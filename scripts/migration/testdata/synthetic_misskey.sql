-- Synthetic Misskey-shaped database for exporter memory/selection tests.
-- psql -v remote_notes=200000 -v local_notes=2000 -f synthetic_misskey.sql
\set ON_ERROR_STOP on
\if :{?remote_notes} \else \set remote_notes 200000 \endif
\if :{?local_notes} \else \set local_notes 2000 \endif
DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;
CREATE FUNCTION mkid(n bigint) RETURNS varchar LANGUAGE sql IMMUTABLE AS $$ SELECT lpad(n::text, 16, '0') $$;
CREATE TABLE "user"(id varchar(32) PRIMARY KEY, "createdAt" timestamptz NOT NULL DEFAULT now(), "updatedAt" timestamptz, username varchar(128) NOT NULL,
  host varchar(512), name varchar(128), uri varchar(512), inbox varchar(512), "avatarId" varchar(32), "bannerId" varchar(32), emojis varchar(128)[] NOT NULL DEFAULT '{}',
  "isLocked" boolean NOT NULL DEFAULT false, "isBot" boolean NOT NULL DEFAULT false);
CREATE INDEX ON "user"(host);
CREATE TABLE user_profile("userId" varchar(32) PRIMARY KEY, description text, password varchar(128), email varchar(128), fields jsonb NOT NULL DEFAULT '[]');
CREATE TABLE user_keypair("userId" varchar(32) PRIMARY KEY, "publicKey" text, "privateKey" text);
CREATE TABLE user_publickey("userId" varchar(32) PRIMARY KEY, "keyId" varchar(512), "keyPem" text);
CREATE TABLE drive_file(id varchar(32) PRIMARY KEY, "createdAt" timestamptz NOT NULL DEFAULT now(), "userId" varchar(32), "userHost" varchar(512), name varchar(256), type varchar(128),
  size int NOT NULL DEFAULT 0, url varchar(512), "storedInternal" boolean NOT NULL DEFAULT false, "isLink" boolean NOT NULL DEFAULT true, "isSensitive" boolean NOT NULL DEFAULT false, properties jsonb NOT NULL DEFAULT '{}');
CREATE INDEX ON drive_file("userId");
CREATE TABLE note(id varchar(32) PRIMARY KEY, "createdAt" timestamptz NOT NULL DEFAULT now(), "updatedAt" timestamptz, "userId" varchar(32) NOT NULL, "userHost" varchar(512),
  "replyId" varchar(32), "renoteId" varchar(32), "threadId" varchar(256), text text, cw varchar(512), visibility varchar(16) NOT NULL DEFAULT 'public',
  "localOnly" boolean NOT NULL DEFAULT false, "visibleUserIds" varchar(32)[] NOT NULL DEFAULT '{}', "fileIds" varchar(32)[] NOT NULL DEFAULT '{}', tags varchar(128)[] NOT NULL DEFAULT '{}',
  emojis varchar(128)[] NOT NULL DEFAULT '{}', uri varchar(512), url varchar(512), "hasPoll" boolean NOT NULL DEFAULT false, "channelId" varchar(32));
CREATE INDEX ON note("userHost"); CREATE INDEX ON note("replyId"); CREATE INDEX ON note("renoteId"); CREATE INDEX ON note("userId");
CREATE TABLE following(id varchar(32) PRIMARY KEY, "createdAt" timestamptz NOT NULL DEFAULT now(), "followerId" varchar(32) NOT NULL, "followeeId" varchar(32) NOT NULL,
  "followerHost" varchar(512), "followeeHost" varchar(512), notify varchar(32));
CREATE TABLE note_reaction(id varchar(32) PRIMARY KEY, "createdAt" timestamptz NOT NULL DEFAULT now(), "userId" varchar(32) NOT NULL, "noteId" varchar(32) NOT NULL, reaction varchar(260) NOT NULL);
CREATE INDEX ON note_reaction("noteId"); CREATE INDEX ON note_reaction("userId");
CREATE TABLE note_favorite(id varchar(32) PRIMARY KEY, "createdAt" timestamptz NOT NULL DEFAULT now(), "userId" varchar(32) NOT NULL, "noteId" varchar(32) NOT NULL);
CREATE TABLE user_note_pining(id varchar(32) PRIMARY KEY, "createdAt" timestamptz NOT NULL DEFAULT now(), "userId" varchar(32) NOT NULL, "noteId" varchar(32) NOT NULL);
CREATE TABLE poll("noteId" varchar(32) PRIMARY KEY, "expiresAt" timestamptz, multiple boolean NOT NULL DEFAULT false, choices varchar(256)[] NOT NULL, votes int[] NOT NULL, "userId" varchar(32), "userHost" varchar(512));
CREATE TABLE poll_vote(id varchar(32) PRIMARY KEY, "createdAt" timestamptz NOT NULL DEFAULT now(), "userId" varchar(32) NOT NULL, "noteId" varchar(32) NOT NULL, choice int NOT NULL);
CREATE TABLE emoji(id varchar(32) PRIMARY KEY, name varchar(128) NOT NULL, host varchar(512), "originalUrl" varchar(512), "publicUrl" varchar(512), type varchar(64), aliases varchar(128)[] NOT NULL DEFAULT '{}', category varchar(128));
CREATE INDEX ON emoji(host);
CREATE TABLE instance(id varchar(32) PRIMARY KEY, host varchar(512) NOT NULL UNIQUE, "softwareName" varchar(64));
CREATE TABLE meta(id varchar(32) PRIMARY KEY, name varchar(1024), policies jsonb NOT NULL DEFAULT '{}');
CREATE TABLE "__chart__notes"(id serial PRIMARY KEY, date int NOT NULL, "___local_inc" int NOT NULL DEFAULT 0);

-- 50 local users (ids 1..50), 5000 remote users (ids 1001..6000) on 100 hosts
INSERT INTO "user"(id, username, host, uri, "createdAt")
SELECT mkid(n), 'local' || n, NULL, NULL, now() - interval '400 days' FROM generate_series(1, 50) n;
-- Misskey system actors
INSERT INTO "user"(id, username, host, uri, "createdAt") VALUES (mkid(60), 'instance.actor', NULL, NULL, now() - interval '500 days'), (mkid(61), 'relay.actor', NULL, NULL, now() - interval '500 days');
CREATE TABLE relay(id varchar(32) PRIMARY KEY, inbox varchar(512) NOT NULL UNIQUE, status varchar(16) NOT NULL);
INSERT INTO relay VALUES (mkid(450000001), 'https://relay.test/inbox', 'accepted');
INSERT INTO "user"(id, username, host, uri, inbox, "createdAt", "avatarId")
SELECT mkid(n), 'r' || n, 'host' || (n % 100) || '.test', 'https://host' || (n % 100) || '.test/users/' || n, 'https://host' || (n % 100) || '.test/inbox',
       now() - interval '300 days', CASE WHEN n % 10 = 0 THEN mkid(900000000 + n) END
FROM generate_series(1001, 6000) n;
INSERT INTO user_profile("userId", description, password, email)
SELECT id, 'profile of ' || username, CASE WHEN host IS NULL THEN '$2b$10$abcdefghijklmnopqrstuuJ1yJ2b9i0qT2qYb5j4q8v7K3l1t7vXe' END, CASE WHEN host IS NULL THEN username || '@example.test' END FROM "user";
INSERT INTO user_keypair SELECT id, 'PUB-' || id, 'PRIV-' || id FROM "user" WHERE host IS NULL;
INSERT INTO user_publickey SELECT id, uri || '#main-key', 'PEM-' || id FROM "user" WHERE host IS NOT NULL;
INSERT INTO instance SELECT mkid(800000000 + n), 'host' || n || '.test', 'misskey' FROM generate_series(0, 99) n;
INSERT INTO meta VALUES ('x', 'Synthetic', '{}');
INSERT INTO emoji SELECT mkid(700000000 + n), 'local_e' || n, NULL, 'https://src.test/files/e' || n, 'https://src.test/files/e' || n, 'image/png' FROM generate_series(1, 20) n;
INSERT INTO emoji SELECT mkid(710000000 + n), 'remote_e' || n, 'host' || (n % 200) || '.test', 'https://h/e' || n, 'https://h/e' || n, 'image/png' FROM generate_series(1, 4000) n;
-- remote avatars (linked)
INSERT INTO drive_file(id, "userId", "userHost", name, url, "isLink") SELECT mkid(900000000 + n), mkid(n), 'host' || (n % 100) || '.test', 'avatar', 'https://h/a' || n, true FROM generate_series(1001, 6000) n WHERE n % 10 = 0;
-- remote notes 100000001.. (the remote cache), every 4th has a linked file
INSERT INTO note(id, "userId", "userHost", text, uri, "createdAt", "fileIds")
SELECT mkid(100000000 + n), mkid(1001 + n % 5000), 'host' || ((1001 + n % 5000) % 100) || '.test',
       'remote note ' || n || ' ' || repeat('lorem ipsum dolor sit amet ', 20), 'https://h/notes/' || n, now() - interval '1 day' * (n % 300),
       CASE WHEN n % 4 = 0 THEN ARRAY[mkid(500000000 + n)] ELSE '{}' END
FROM generate_series(1, :remote_notes) n;
INSERT INTO drive_file(id, "userId", "userHost", name, url, "isLink") SELECT mkid(500000000 + n), mkid(1001 + n % 5000), 'host.test', 'remote file', 'https://h/f' || n, true FROM generate_series(4, :remote_notes, 4) n;
-- local notes 200000001..: every 5th replies to a remote note, every 7th quotes one
INSERT INTO note(id, "userId", "userHost", text, "replyId", "renoteId", "createdAt", visibility)
SELECT mkid(200000000 + n), mkid(1 + n % 50), NULL, 'local note ' || n || ' :local_e1:',
       CASE WHEN n % 5 = 0 THEN mkid(100000000 + n * 7 % :remote_notes + 1) END,
       CASE WHEN n % 7 = 0 THEN mkid(100000000 + n * 13 % :remote_notes + 1) END,
       now() - interval '1 hour' * n, 'public'
FROM generate_series(1, :local_notes) n;
UPDATE note SET "threadId" = "replyId" WHERE "replyId" IS NOT NULL AND "userHost" IS NULL;
-- remote replies to local notes
INSERT INTO note(id, "userId", "userHost", text, "replyId", "threadId", uri)
SELECT mkid(300000000 + n), mkid(1001 + n), 'host' || ((1001 + n) % 100) || '.test', 'remote reply', mkid(200000000 + n * 3), mkid(200000000 + n * 3), 'https://h/r/' || n
FROM generate_series(1, :local_notes / 10) n;
-- follows: each local user follows 40 remote users and is followed by 40 others; remote<->remote rows never exist in Misskey
INSERT INTO following(id, "followerId", "followeeId", "followerHost", "followeeHost")
SELECT mkid(600000000 + l * 100 + k), mkid(l), mkid(1001 + (l * 97 + k) % 5000), NULL, 'h' FROM generate_series(1, 50) l, generate_series(1, 40) k;
INSERT INTO following(id, "followerId", "followeeId", "followerHost", "followeeHost")
SELECT mkid(610000000 + l * 100 + k), mkid(1001 + (l * 31 + k * 7) % 5000), mkid(l), 'h', NULL FROM generate_series(1, 50) l, generate_series(1, 40) k
ON CONFLICT DO NOTHING;
-- reactions: on local notes by remote users, and remote-on-remote cache rows (must be excluded by default)
INSERT INTO note_reaction(id, "userId", "noteId", reaction) SELECT mkid(400000000 + n), mkid(1001 + n % 5000), mkid(200000000 + 1 + n % :local_notes), ':like:' FROM generate_series(1, 5000) n;
INSERT INTO note_reaction(id, "userId", "noteId", reaction) SELECT mkid(410000000 + n), mkid(1001 + n % 5000), mkid(100000000 + 1 + n % :remote_notes), ':like:' FROM generate_series(1, 50000) n;
INSERT INTO note_favorite(id, "userId", "noteId") SELECT mkid(420000000 + n), mkid(1 + n % 50), mkid(100000000 + n * 101 % :remote_notes + 1) FROM generate_series(1, 100) n;
INSERT INTO user_note_pining(id, "userId", "noteId") SELECT mkid(430000000 + n), mkid(1 + n % 50), mkid(200000000 + n) FROM generate_series(1, 50) n;
-- three polls by one author (poll has no id column; PK is noteId) and a
-- multiple-choice vote stored as one row per chosen option
INSERT INTO poll("noteId", multiple, choices, votes, "userId") SELECT mkid(200000000 + n), true, ARRAY['a','b'], ARRAY[1,1], mkid(1) FROM generate_series(50, 150, 50) n;
INSERT INTO poll_vote(id, "userId", "noteId", choice) VALUES (mkid(440000001), mkid(2), mkid(200000050), 0), (mkid(440000002), mkid(2), mkid(200000050), 1);
INSERT INTO "__chart__notes"(date) SELECT n FROM generate_series(1, 1000) n;
ANALYZE;
