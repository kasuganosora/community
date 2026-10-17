-- Small Mastodon-shaped database for exporter selection tests.
\set ON_ERROR_STOP on
DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;
CREATE TABLE schema_migrations(version varchar PRIMARY KEY);
INSERT INTO schema_migrations VALUES ('20250101000000');
CREATE TABLE accounts(id bigint PRIMARY KEY, username varchar NOT NULL, domain varchar, uri varchar, url varchar, display_name varchar, note text,
  public_key text, private_key text, created_at timestamp NOT NULL DEFAULT now(), updated_at timestamp NOT NULL DEFAULT now(), avatar_file_name varchar, avatar_remote_url varchar, fields jsonb);
CREATE TABLE users(id bigint PRIMARY KEY, account_id bigint, email varchar, encrypted_password varchar, confirmed_at timestamp, role_id bigint, created_at timestamp DEFAULT now());
CREATE TABLE statuses(id bigint PRIMARY KEY, account_id bigint NOT NULL, text text, uri varchar, url varchar, in_reply_to_id bigint, reblog_of_id bigint,
  visibility int NOT NULL DEFAULT 0, local boolean, created_at timestamp NOT NULL DEFAULT now(), updated_at timestamp NOT NULL DEFAULT now(), ordered_media_attachment_ids bigint[]);
CREATE INDEX ON statuses(account_id); CREATE INDEX ON statuses(in_reply_to_id);
CREATE TABLE media_attachments(id bigint PRIMARY KEY, status_id bigint, account_id bigint, file_file_name varchar, file_file_size int, remote_url varchar, created_at timestamp DEFAULT now());
CREATE TABLE follows(id bigint PRIMARY KEY, account_id bigint NOT NULL, target_account_id bigint NOT NULL, uri varchar, created_at timestamp DEFAULT now());
CREATE TABLE favourites(id bigint PRIMARY KEY, account_id bigint NOT NULL, status_id bigint NOT NULL, created_at timestamp DEFAULT now());
CREATE TABLE mentions(id bigint PRIMARY KEY, status_id bigint, account_id bigint, silent boolean NOT NULL DEFAULT false);
CREATE TABLE tags(id bigint PRIMARY KEY, name varchar NOT NULL);
CREATE TABLE statuses_tags(status_id bigint NOT NULL, tag_id bigint NOT NULL);
CREATE TABLE custom_emojis(id bigint PRIMARY KEY, shortcode varchar NOT NULL, domain varchar, image_file_name varchar, image_remote_url varchar, uri varchar);
CREATE TABLE preview_cards(id bigint PRIMARY KEY, url varchar);
INSERT INTO accounts(id, username, domain, uri, public_key, private_key) VALUES (1, 'alice', NULL, NULL, 'PUB', 'PRIV'), (2, 'bob', NULL, NULL, 'PUB', 'PRIV');
INSERT INTO accounts(id, username, domain, uri, public_key) SELECT n, 'r' || n, 'remote' || (n % 5) || '.test', 'https://remote' || (n % 5) || '.test/users/' || n, 'PUB' FROM generate_series(100, 1099) n;
INSERT INTO users(id, account_id, email, encrypted_password, confirmed_at) VALUES (1, 1, 'a@x.test', '$2a$10$abcdefghijklmnopqrstuuJ1yJ2b9i0qT2qYb5j4q8v7K3l1t7vXe', now()), (2, 2, 'b@x.test', '$2a$10$abcdefghijklmnopqrstuuJ1yJ2b9i0qT2qYb5j4q8v7K3l1t7vXe', now());
-- 20000 remote statuses (cache), 50 local statuses replying to / boosting remote ones, 10 remote replies to local
INSERT INTO statuses(id, account_id, text, uri) SELECT 100000 + n, 100 + n % 1000, 'remote ' || n, 'https://remote.test/s/' || n FROM generate_series(1, 20000) n;
INSERT INTO statuses(id, account_id, text, local, in_reply_to_id, reblog_of_id) SELECT 500000 + n, 1 + n % 2, 'local :blob: ' || n, true,
  CASE WHEN n % 5 = 0 THEN 100000 + n * 3 END, CASE WHEN n % 7 = 0 THEN 100000 + n * 11 END FROM generate_series(1, 50) n;
INSERT INTO statuses(id, account_id, text, uri, in_reply_to_id) SELECT 600000 + n, 200 + n, 'remote reply', 'https://remote.test/r/' || n, 500000 + n FROM generate_series(1, 10) n;
INSERT INTO media_attachments(id, status_id, account_id, remote_url) SELECT 700000 + n, 100000 + n, 100 + n % 1000, 'https://remote.test/m/' || n FROM generate_series(1, 5000) n;
INSERT INTO follows SELECT n, 1, 100 + n, 'https://x.test/f/' || n FROM generate_series(1, 30) n;
INSERT INTO follows SELECT 1000 + n, 300 + n, 2, NULL FROM generate_series(1, 30) n;
INSERT INTO follows SELECT 5000 + n, 500 + n, 600 + n, NULL FROM generate_series(1, 300) n;  -- remote<->remote (never needed)
INSERT INTO favourites SELECT n, 400 + n, 500000 + 1 + n % 50 FROM generate_series(1, 40) n;
INSERT INTO favourites SELECT 1000 + n, 100 + n % 1000, 100000 + n FROM generate_series(1, 3000) n;
INSERT INTO mentions SELECT n, 100000 + n, 100 + n % 1000 FROM generate_series(1, 5000) n;
INSERT INTO tags SELECT n, 'tag' || n FROM generate_series(1, 100) n;
INSERT INTO statuses_tags SELECT 100000 + n, 1 + n % 100 FROM generate_series(1, 5000) n;
INSERT INTO statuses_tags SELECT 500000 + n, 1 FROM generate_series(1, 50) n;
INSERT INTO custom_emojis VALUES (1, 'blob', NULL, NULL, NULL, NULL);
INSERT INTO custom_emojis SELECT n, 'e' || n, 'remote' || (n % 50) || '.test', NULL, 'https://r/e' || n, 'https://r/e' || n FROM generate_series(2, 2000) n;
INSERT INTO preview_cards SELECT n, 'https://x/' || n FROM generate_series(1, 3000) n;
ANALYZE;
