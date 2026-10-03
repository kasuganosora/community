# frozen_string_literal: true

# Run inside the ORIGINAL Mastodon Rails environment:
#   TRANSFER_OUTPUT=/private/new-directory bin/rails runner /path/export_mastodon.rb
# This is a site-authority export, NOT a personal archive converter.
require 'json'
require 'date'
require 'uri'
require 'digest'
require 'fileutils'
require 'securerandom'

class CommunityMastodonExport
  SECTIONS = %w[accounts credentials keys posts media relations lists reactions bookmarks polls poll_votes emojis records].freeze

  def initialize
    @issues = []
    @counts = {}
    @media = []
    @avatar_ids = {}
    @header_ids = {}
    @emoji_media = {}
    @root = File.expand_path(ENV.fetch('TRANSFER_OUTPUT'))
    raise ArgumentError, 'output must be a new directory' if File.exist?(@root) || File.symlink?(@root)
    # Require an existing trusted parent; never traverse a supplied symlink.
    parent = File.dirname(@root)
    raise ArgumentError, 'output parent must exist without symlink components' unless File.directory?(parent) && File.realpath(parent) == parent
    Dir.mkdir(@root, 0o700)
    Dir.mkdir(File.join(@root, 'media'), 0o700)
  end

  def issue(severity, code, kind, id, message)
    @issues << { severity: severity, code: code, kind: kind, source_id: id.to_s, message: message }
  end

  def time(value)
    value&.utc&.iso8601(6)
  end

  def normalize(value, key = nil)
    case value
    when Hash then value.to_h { |k, v| [k, normalize(v, k.to_s)] }
    when Array then value.map { |v| normalize(v, key&.sub(/s\z/, '')) }
    when Time, DateTime then time(value)
    when Date then value.iso8601
    when Integer then key == 'id' || key&.end_with?('_id') ? value.to_s : value
    else value
    end
  end

  def section(io, name)
    io.write(",#{name.to_json}:[")
    first = true
    emit = lambda do |row|
      io.write(',') unless first
      first = false
      io.write(JSON.generate(row))
      @counts[name] = @counts.fetch(name, 0) + 1
    end
    yield emit
    io.write(']')
  end

  def each(model, &block)
    model.unscoped.find_each(batch_size: 250, &block)
  end

  def asset(id, owner, attachment, description: nil, remote_url: nil, blurhash: nil, meta: nil, created_at: nil, style: :original)
    return nil if attachment.blank? || attachment.original_filename.blank?
    relative = "media/#{Digest::SHA256.hexdigest(id)}"
    destination = File.join(@root, relative)
    adapter = nil
    begin
      adapter = Paperclip.io_adapters.for(style == :original ? attachment : attachment.styles.fetch(style))
      digest = Digest::SHA256.new
      size = 0
      File.open(destination, File::WRONLY | File::CREAT | File::EXCL, 0o600) do |out|
        out.binmode
        while (chunk = adapter.read(1024 * 1024))
          break if chunk.empty?
          out.write(chunk)
          digest.update(chunk)
          size += chunk.bytesize
        end
      end
      raise IOError, 'attachment length mismatch' if style == :original && attachment.size && size != attachment.size.to_i
      m = meta || {}
      original = m['original'] || m[:original] || {}
      focus = m['focus'] || m[:focus] || {}
      @media << { id: id, owner_id: owner.to_s, path: relative, sha256: digest.hexdigest,
                  size: size, original_url: URI.join(@base_url || "https://example.invalid", attachment.url(style)).to_s, name: File.basename(attachment.original_filename),
                  content_type: (style == :original ? attachment.content_type.to_s : ({ png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', webp: 'image/webp', gif: 'image/gif' }[attachment.styles.fetch(style).format&.to_sym] || attachment.content_type.to_s)), description: description.to_s,
                  remote_url: remote_url.to_s, blurhash: blurhash.to_s,
                  width: (original['width'] || original[:width] || 0).to_i,
                  height: (original['height'] || original[:height] || 0).to_i,
                  focus_x: (focus['x'] || focus[:x] || 0).to_f,
                  focus_y: (focus['y'] || focus[:y] || 0).to_f,
                  created_at: time(created_at) }.compact
      if style == :original && attachment.respond_to?(:styles)
        attachment.styles.each_key do |variant|
          next if variant.to_sym == :original
          if attachment.exists?(variant)
            asset("#{id}:style:#{variant}", owner, attachment, description: description, created_at: created_at, style: variant)
          else
            issue('warning', 'source_style_absent', 'media', id, 'Configured derived style is absent in source storage; no old bytes were fabricated.')
          end
        end
      end
      id
    rescue StandardError => error
      File.delete(destination) if File.exist?(destination)
      issue('error', 'missing_or_unreadable_media', 'media', id, "Source attachment could not be copied and verified (#{error.class}); inspect source storage privately.")
      nil
    ensure
      adapter.close if adapter.respond_to?(:close)
    end
  end

  def run
    connection = ActiveRecord::Base.connection
    # Snapshot covers all table reads; operator must freeze media writes for a byte-consistent cutover.
    connection.transaction(isolation: :repeatable_read) do
      connection.execute('SET TRANSACTION READ ONLY')
      raise 'read-only snapshot unavailable' unless connection.select_value('SHOW transaction_read_only') == 'on' && connection.select_value('SHOW transaction_isolation') == 'repeatable read'
      source = { platform: 'mastodon', version: Mastodon::Version.to_s,
                 base_url: "https://#{Rails.configuration.x.web_domain || Rails.configuration.x.local_domain}",
                 account_domain: Rails.configuration.x.local_domain,
                 exported_at: Time.now.utc.iso8601(6), snapshot_id: connection.select_value('SELECT txid_current_snapshot()::text').to_s }
      issue('error', 'password_pepper_unsupported', 'credentials', '', 'Source Devise pepper is configured; target compatibility requires a separately protected verifier secret.') if User.pepper.present?
      @base_url = source[:base_url]
      gather_media
      path = File.join(@root, 'bundle.json.partial')
      File.open(path, File::WRONLY | File::CREAT | File::EXCL, 0o600) do |io|
        io.write(JSON.generate(schema: 'community-transfer/v1', scope: 'site', source: source).delete_suffix('}'))
        SECTIONS.each { |name| section(io, name) { |emit| public_send("export_#{name}", emit) } }
        io.write(',"issues":')
        io.write(JSON.generate(@issues))
        io.write('}')
        io.flush
        io.fsync
      end
      File.rename(path, File.join(@root, 'bundle.json'))
    end
    errors = @issues.count { |i| i[:severity] == 'error' }
    puts JSON.generate(platform: 'mastodon', version: Mastodon::Version.to_s, counts: @counts, errors: errors, warnings: @issues.count { |i| i[:severity] == 'warning' })
    errors.zero? ? 0 : 2
  end

  def gather_media
    each(Account) do |a|
      @avatar_ids[a.id] = asset("account:#{a.id}:avatar", a.id, a.avatar, description: a.avatar_description, created_at: a.created_at)
      @header_ids[a.id] = asset("account:#{a.id}:header", a.id, a.header, description: a.header_description, created_at: a.created_at)
    end
    each(MediaAttachment) do |m|
      if m.file.original_filename.blank? && !m.account&.local? && m.remote_url.present?
        remote_media(m)
      else
        copied = asset(m.id.to_s, m.account_id, m.file, description: m.description, remote_url: m.remote_url, blurhash: m.blurhash, meta: m.file_meta, created_at: m.created_at)
        issue('error', 'media_has_no_original', 'media', m.id, 'Media record has no original file; original bytes are required.') unless copied
      end
      asset("media:#{m.id}:thumbnail", m.account_id, m.thumbnail, remote_url: m.thumbnail_remote_url, created_at: m.created_at)
    end
    system_actor = Account.local.find { |a| a.instance_actor? }
    each(CustomEmoji) do |e|
      @emoji_media[e.id] = asset("emoji:#{e.id}", system_actor&.id, e.image, remote_url: e.image_remote_url, created_at: e.created_at)
      issue('error', 'emoji_has_no_original', 'emojis', e.id, 'Emoji image is missing; cannot silently drop it.') unless @emoji_media[e.id]
    end
  end

  def remote_media(media)
    url = URI.parse(media.remote_url)
    unless url.is_a?(URI::HTTPS) && url.host.present? && url.userinfo.nil? && !media.remote_url.match?(/[\x00-\x20\x7f]/)
      raise URI::InvalidURIError
    end
    @media << { id: media.id.to_s, owner_id: media.account_id.to_s, size: 0,
                name: "remote-#{media.id}", content_type: media.file_content_type.presence || 'application/octet-stream',
                remote_url: media.remote_url, description: media.description.to_s,
                blurhash: media.blurhash.to_s, created_at: time(media.created_at) }
    issue('warning', 'remote_media_reference', 'media', media.id, 'Source has only an uncached remote HTTPS reference; no bytes fetched or checksum claimed. Original type and metadata remain in Records.')
  rescue URI::InvalidURIError
    issue('error', 'invalid_remote_media_reference', 'media', media.id, 'Uncached remote reference is not a valid credential-free HTTPS URL.')
  end

  def export_accounts(emit)
    each(Account) do |a|
      fields = (a[:fields] || []).map { |f| { name: f['name'].to_s, value: f['value'].to_s, verified_at: f['verified_at'] }.compact }
      emit.call(id: a.id.to_s, username: a.username, domain: a.domain.to_s, local: a.local?, system: a.instance_actor?,
                actor_uri: ActivityPub::TagManager.instance.uri_for(a), url: ActivityPub::TagManager.instance.url_for(a),
                inbox: a.local? ? ActivityPub::TagManager.instance.inbox_uri_for(a) : a.inbox_url,
                shared_inbox: a.shared_inbox_url, display_name: a.display_name, note: a.note,
                note_html: !a.local?, fields: fields, emoji_ids: a.emojis.map { |emoji| emoji.id.to_s }, avatar_id: @avatar_ids[a.id], header_id: @header_ids[a.id],
                locked: a.locked?, bot: a.actor_type == 'Service', discoverable: !!a.discoverable,
                noindex: !a.indexable, suspended: a.suspended_at.present?, admin: !!a.user&.role&.can?(:administrator),
                created_at: time(a.created_at), updated_at: time(a.updated_at),
                settings: { source_id_scheme: a.id_scheme, source_actor_type: a.actor_type, source_without_login: a.local? && a.user.nil?,
                            source_requested_deletion_at: time(a.requested_deletion_at),
                            source_moved_to_account_id: a.moved_to_account_id&.to_s,
                            source_also_known_as: a.also_known_as || [], source_memorial: a.memorial?,
                            source_silenced_at: time(a.silenced_at) })
      if a.local? && a.user.nil?
        issue(a.instance_actor? ? 'warning' : 'error', 'local_actor_without_login', 'accounts', a.id, 'Local actor has no User; only the official instance actor is a system identity, never manufacture credentials.')
      end
    end
  end

  def export_credentials(emit)
    each(User) do |u|
      two_factor = u.otp_required_for_login? || u.webauthn_credentials.exists?
      issue('error', 'two_factor_requires_adapter', 'credentials', u.account_id, 'TOTP/WebAuthn must remain enforced; target adapter is required before account activation.') if two_factor
      issue('error', 'missing_password_verifier', 'credentials', u.account_id, 'No portable password verifier; external identity provider or secure recovery is required.') if u.encrypted_password.blank?
      # Decrypt through official Rails API; only the private bundle receives secrets.
      ext = if two_factor
              { 'otp_secret_plaintext' => u.otp_secret, 'consumed_timestep' => u.consumed_timestep,
                'otp_backup_codes' => u.otp_backup_codes, 'webauthn_required' => u.webauthn_credentials.exists? }
            elsif User.pepper.present?
              { 'pepper_required' => true }
            end
      emit.call(account_id: u.account_id.to_s, email: u.email, email_verified: u.confirmed_at.present?,
                password_hash: u.encrypted_password, password_scheme: 'bcrypt',
                disabled: u.disabled? || !u.approved?, two_factor: two_factor, extensions: ext)
    end
  end

  def export_keys(emit)
    each(Account) do |a|
      keys = a.keypairs.order(:id).to_a
      keys << Keypair.from_legacy_account(a) if keys.empty? && a.public_key.present?
      primary = keys.find { |k| k.type == 'rsa' && k.usable? }
      keys.each do |k|
        algorithm = k.type
        issue('error', 'unsupported_active_key', 'keys', a.id, 'An active non-RSA signing key requires target support.') if a.local? && k.usable? && algorithm != 'rsa'
        # Legacy key's URI is already canonical; modern key full_uri preserves #rsa-*.
        emit.call(account_id: a.id.to_s, key_id: k.full_uri, public_key: k.public_key,
                  private_key: a.local? ? k.private_key : nil, algorithm: algorithm,
                  primary: k.equal?(primary), revoked: k.revoked?, expires_at: time(k.expires_at))
      end
      issue('error', 'missing_local_signing_key', 'keys', a.id, 'Local actor lacks a usable RSA identity key.') if a.local? && primary.nil?
    end
  end

  def export_posts(emit)
    each(Status) do |s|
      issue('error', 'unsupported_limited_visibility', 'posts', s.id, 'Limited visibility cannot be silently mapped to public/private; explicit ACL adapter is required.') if s.visibility == 'limited'
      emit.call({ id: s.id.to_s, author_id: s.account_id.to_s, uri: ActivityPub::TagManager.instance.uri_for(s),
                  url: ActivityPub::TagManager.instance.url_for(s), spoiler_text: s.spoiler_text,
                  visibility: s.visibility, sensitive: s.sensitive?, language: s.language,
                  recipient_ids: s.active_mentions.pluck(:account_id).map(&:to_s),
                  reply_id: s.in_reply_to_id&.to_s, reblog_id: s.reblog_of_id&.to_s,
                  media_ids: (s.ordered_media_attachment_ids.presence || s.media_attachments.order(:id).pluck(:id)).map(&:to_s),
                  tags: s.tags.pluck(:name), emoji_ids: s.emojis.pluck(:id).map(&:to_s),
                  conversation_id: s.conversation_id&.to_s, pinned: StatusPin.where(status_id: s.id).exists?,
                  archive_only: s.deleted_at.present?, created_at: time(s.created_at), updated_at: time(s.updated_at),
                  extensions: normalize(s.attributes) }.merge(s.local? ? { text: s.text } : { html: s.text }))
    end
  end

  def export_media(emit)
    @media.each(&emit)
  end

  def export_relations(emit)
    { Follow => 'follow', FollowRequest => 'follow_request', Block => 'block', Mute => 'mute' }.each do |model, kind|
      each(model) do |r|
        emit.call(kind: kind, account_id: r.account_id.to_s, target_id: r.target_account_id.to_s,
                  target_uri: ActivityPub::TagManager.instance.uri_for(r.target_account),
                  activity_uri: (r.respond_to?(:uri) ? r.uri : nil),
                  languages: (r.respond_to?(:languages) ? r.languages : nil),
                  show_reblogs: r.respond_to?(:show_reblogs) ? r.show_reblogs : true,
                  notify: r.respond_to?(:notify) ? r.notify : false, created_at: time(r.created_at))
      end
    end
    each(AccountDomainBlock) { |r| emit.call(kind: 'domain_block', account_id: r.account_id.to_s, target_domain: r.domain, show_reblogs: false, created_at: time(r.created_at)) }
  end

  def export_lists(emit)
    each(List) { |l| emit.call(id: l.id.to_s, owner_id: l.account_id.to_s, title: l.title, replies_policy: l.replies_policy, exclusive: l.exclusive?, member_ids: l.list_accounts.pluck(:account_id).map(&:to_s)) }
  end

  def export_reactions(emit)
    each(Favourite) { |r| emit.call(id: r.id.to_s, account_id: r.account_id.to_s, post_id: r.status_id.to_s, created_at: time(r.created_at)) }
  end

  def export_bookmarks(emit)
    each(Bookmark) { |r| emit.call(account_id: r.account_id.to_s, post_id: r.status_id.to_s) }
  end

  def export_polls(emit)
    each(Poll) do |p|
      emit.call(id: p.id.to_s, post_id: p.status_id.to_s, multiple: p.multiple?, expires_at: time(p.expires_at),
                options: p.options.each_with_index.map { |title, i| { title: title, votes: p.cached_tallies[i].to_i } })
    end
  end

  def export_poll_votes(emit)
    each(PollVote) { |v| emit.call(poll_id: v.poll_id.to_s, account_id: v.account_id.to_s, option: v.choice, created_at: time(v.created_at)) }
  end

  def export_emojis(emit)
    each(CustomEmoji) { |e| emit.call(id: e.id.to_s, shortcode: e.shortcode, domain: e.domain.to_s, media_id: @emoji_media[e.id], url: e.uri, category: e.category&.name, disabled: e.disabled?) }
  end

  def export_records(emit)
    c = ActiveRecord::Base.connection
    # Keep the source's full raw rows, including unsupported settings, histories,
    # sessions and encrypted fields. Never treat this archive as live conversion.
    c.tables.sort.each do |table|
      next if %w[schema_migrations ar_internal_metadata].include?(table)
      count = 0
      c.execute("DECLARE community_export_rows NO SCROLL CURSOR FOR SELECT row_to_json(source_row)::text AS data FROM #{c.quote_table_name(table)} source_row")
      loop do
        result = c.exec_query('FETCH FORWARD 250 FROM community_export_rows')
        break if result.empty?
        result.each do |record|
          row = JSON.parse(record.fetch('data'))
          count += 1
          emit.call(kind: "mastodon.#{table}", id: (row['id'] || "row:#{count}").to_s,
                    owner_id: row['account_id']&.to_s, data: normalize(row))
        end
      end
      c.execute('CLOSE community_export_rows')
      issue('warning', 'raw_records_preserved', table, '', "#{count} complete source rows retained privately; raw preservation is not operational feature conversion.") if count.positive?
    end
  end
end

if defined?(Rails) && Rails.respond_to?(:application)
begin
  # Suppress ORM/library logs which may include SQL literals or attachment paths.
  Rails.logger = ActiveSupport::Logger.new(File::NULL)
  ActiveRecord::Base.logger = Rails.logger
  Paperclip.options[:log] = false
  exit CommunityMastodonExport.new.run
rescue StandardError, SystemStackError
  # Never serialize exception messages: providers may embed credentials/DSNs.
  warn 'Mastodon export failed; private partial output retained. No source writes were performed.'
  exit 1
end

end
