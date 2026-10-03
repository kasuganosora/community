# frozen_string_literal: true
# Standalone tests: ruby scripts/migration/export_mastodon_test.rb
require 'minitest/autorun'
require 'tmpdir'
require 'stringio'
require_relative 'export_mastodon'

# Only attachment API surface needed by the bounded file copier.
module Paperclip
  def self.io_adapters
    self
  end

  def self.for(attachment)
    StringIO.new(attachment.bytes)
  end
end

class Object
  def present?
    !blank?
  end

  def presence
    self if present?
  end

  def blank?
    respond_to?(:empty?) ? empty? : !self
  end
end

class CommunityMastodonExportTest < Minitest::Test
  Attachment = Struct.new(:bytes, :original_filename, :content_type, :size) do
    def url(_style)
      "/system/original.png"
    end
  end

  def setup
    @tmp = Dir.mktmpdir
    @previous = ENV['TRANSFER_OUTPUT']
    ENV['TRANSFER_OUTPUT'] = File.join(@tmp, 'new')
    @export = CommunityMastodonExport.new
  end

  def teardown
    ENV['TRANSFER_OUTPUT'] = @previous
    FileUtils.remove_entry(@tmp)
  end

  def test_binary_media_is_exact_and_private
    data = "\x89PNG\r\n\xFF\x00".b
    a = Attachment.new(data, '../original.png', 'image/png', data.bytesize)
    assert_equal 'asset1', @export.asset('asset1', '9007199254740993', a)
    record = @export.instance_variable_get(:@media).first
    path = File.join(ENV.fetch('TRANSFER_OUTPUT'), record[:path])
    assert_equal data, File.binread(path)
    assert_equal Digest::SHA256.hexdigest(data), record[:sha256]
    assert_equal 'original.png', record[:name]
    assert_equal 0o600, File.stat(path).mode & 0o777
    assert_equal 0o700, File.stat(ENV.fetch('TRANSFER_OUTPUT')).mode & 0o777
  end

  def test_original_style_entry_does_not_recurse_and_keeps_old_url
    a = Attachment.new('abc', 'image.png', 'image/png', 3)
    def a.styles
      { original: nil }
    end
    assert_equal 'style-safe', @export.asset('style-safe', '1', a)
    assert_equal 1, @export.instance_variable_get(:@media).length
    assert_equal 'https://example.invalid/system/original.png', @export.instance_variable_get(:@media).first[:original_url]
  end

  def test_mismatched_length_fails_without_retaining_partial_media
    a = Attachment.new('abc', 'x', 'image/png', 9)
    assert_nil @export.asset('bad', '1', a)
    assert_empty @export.instance_variable_get(:@media)
    assert_empty Dir.children(File.join(ENV.fetch('TRANSFER_OUTPUT'), 'media'))
    assert_equal 'error', @export.instance_variable_get(:@issues).first[:severity]
  end

  def test_source_bigints_and_array_ids_are_strings_but_counts_remain_numeric
    result = @export.normalize('id' => 117374933835518885, 'account_id' => 9007199254740993,
                               'participant_account_ids' => [9007199254740993], 'votes_count' => 7)
    assert_equal '117374933835518885', result['id']
    assert_equal '9007199254740993', result['account_id']
    assert_equal ['9007199254740993'], result['participant_account_ids']
    assert_equal 7, result['votes_count']
  end

  def test_uncached_remote_reference_has_no_fabricated_file_or_checksum
    klass = Struct.new(:id, :account_id, :remote_url, :file_content_type, :description, :blurhash, :created_at)
    @export.remote_media(klass.new(12, 34, 'https://remote.example/image.png'))
    record = @export.instance_variable_get(:@media).first
    assert_equal 'https://remote.example/image.png', record[:remote_url]
    refute record.key?(:path)
    refute record.key?(:sha256)
    assert_equal 'warning', @export.instance_variable_get(:@issues).first[:severity]
    @export.remote_media(klass.new(13, 34, 'https://user:secret@remote.example/image.png'))
    assert_equal 1, @export.instance_variable_get(:@media).length
    assert_equal 'error', @export.instance_variable_get(:@issues).last[:severity]
    refute_includes @export.instance_variable_get(:@issues).last[:message], 'secret'
  end

  def test_existing_destination_and_symlink_parent_are_refused
    assert_raises(ArgumentError) { CommunityMastodonExport.new }
    File.symlink(@tmp, File.join(@tmp, 'alias'))
    ENV['TRANSFER_OUTPUT'] = File.join(@tmp, 'alias', 'other')
    assert_raises(ArgumentError) { CommunityMastodonExport.new }
  end
end
