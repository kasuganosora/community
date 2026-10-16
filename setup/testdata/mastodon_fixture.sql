-- Minimal Mastodon (4.x) schema fixture for the setup wizard migration
-- tests: only the tables and columns export_mastodon_db.py reads. Keys and the
-- password hash ("Mastodon-Pass-1") are test-only values. Load into an empty
-- PostgreSQL database. Source site: http://localhost:18083 (local status URIs
-- reveal it to the wizard). Media lives in testdata/mastodon-system.
CREATE TABLE schema_migrations (version varchar PRIMARY KEY);
CREATE TABLE accounts (id bigint PRIMARY KEY, username varchar NOT NULL, domain varchar, display_name varchar DEFAULT '', note text DEFAULT '',
  uri varchar DEFAULT '', url varchar, inbox_url varchar DEFAULT '', shared_inbox_url varchar DEFAULT '', public_key text DEFAULT '', private_key text,
  locked boolean DEFAULT false, actor_type varchar, discoverable boolean, indexable boolean DEFAULT false, hide_collections boolean,
  fields jsonb, suspended_at timestamp, silenced_at timestamp, moved_to_account_id bigint, also_known_as varchar[],
  avatar_file_name varchar, avatar_content_type varchar, avatar_file_size integer, avatar_remote_url varchar,
  header_file_name varchar, header_content_type varchar, header_file_size integer, header_remote_url varchar DEFAULT '',
  created_at timestamp NOT NULL, updated_at timestamp NOT NULL);
CREATE TABLE user_roles (id bigint PRIMARY KEY, name varchar, permissions bigint DEFAULT 0);
CREATE TABLE users (id bigint PRIMARY KEY, account_id bigint, email varchar DEFAULT '', encrypted_password varchar DEFAULT '', confirmed_at timestamp,
  disabled boolean DEFAULT false, approved boolean DEFAULT true, otp_required_for_login boolean DEFAULT false, role_id bigint, locale varchar);
CREATE TABLE statuses (id bigint PRIMARY KEY, uri varchar, text text DEFAULT '', created_at timestamp NOT NULL, updated_at timestamp NOT NULL,
  in_reply_to_id bigint, reblog_of_id bigint, url varchar, sensitive boolean DEFAULT false, visibility integer DEFAULT 0, spoiler_text text DEFAULT '',
  reply boolean DEFAULT false, language varchar, conversation_id bigint, local boolean, account_id bigint NOT NULL, deleted_at timestamp,
  ordered_media_attachment_ids bigint[]);
CREATE TABLE media_attachments (id bigint PRIMARY KEY, status_id bigint, account_id bigint, file_file_name varchar, file_content_type varchar,
  file_file_size integer, remote_url varchar DEFAULT '', description text, blurhash varchar, file_meta json, type integer DEFAULT 0, created_at timestamp NOT NULL);
CREATE TABLE mentions (id bigint PRIMARY KEY, status_id bigint, account_id bigint, silent boolean DEFAULT false);
CREATE TABLE tags (id bigint PRIMARY KEY, name varchar NOT NULL);
CREATE TABLE statuses_tags (status_id bigint, tag_id bigint);
CREATE TABLE status_pins (id bigint PRIMARY KEY, account_id bigint, status_id bigint);
CREATE TABLE follows (id bigint PRIMARY KEY, account_id bigint NOT NULL, target_account_id bigint NOT NULL, show_reblogs boolean DEFAULT true,
  uri varchar, notify boolean DEFAULT false, languages varchar[], created_at timestamp NOT NULL);
CREATE TABLE favourites (id bigint PRIMARY KEY, account_id bigint, status_id bigint, created_at timestamp NOT NULL);
CREATE TABLE bookmarks (id bigint PRIMARY KEY, account_id bigint, status_id bigint);
CREATE TABLE polls (id bigint PRIMARY KEY, account_id bigint, status_id bigint, expires_at timestamp, options varchar[] DEFAULT '{}',
  cached_tallies bigint[] DEFAULT '{}', multiple boolean DEFAULT false);
CREATE TABLE poll_votes (id bigint PRIMARY KEY, account_id bigint, poll_id bigint, choice integer, created_at timestamp NOT NULL);
CREATE TABLE custom_emoji_categories (id bigint PRIMARY KEY, name varchar);
CREATE TABLE custom_emojis (id bigint PRIMARY KEY, shortcode varchar NOT NULL, domain varchar, image_file_name varchar, image_content_type varchar,
  image_file_size integer, image_remote_url varchar, uri varchar, disabled boolean DEFAULT false, category_id bigint);

INSERT INTO schema_migrations VALUES ('20250601000000');
INSERT INTO user_roles VALUES (-99, '', 0), (3, 'Owner', 1);
INSERT INTO accounts (id, username, domain, public_key, private_key, actor_type, created_at, updated_at) VALUES
 (-99, 'localhost:18083', NULL, '-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEApSDg1MZKWhdCr6a+muPP
WRST2ihqkgG1ZQsy4x7OtV8y8NODqFTkLFX0U9HzlecS8pGb4FRXfhbsYiBuQpwj
X3A3w4bm6v0cwYKAs6Z3EKYXyW0IOEvGwMT2AgERjibKSK/5e5fWCbV6hX4WPruH
d7NoW4eXmZ5KquHlspVUsYykBOkwO1NvG1/BdiyIs1lojH/w0d7lzHv9X33K9471
04ne/psZSBE6O4ER9HLNn2OC+k/Y6i9k9M8tc9ol89DjqLf2A3Yl4kr9Kc+fLhFc
bDL/I/ZGR2O5wjWZ2kluczruovEu9qVbKQlGXdsgF/SjJSX7NvEx/FbeWyh+H93U
EwIDAQAB
-----END PUBLIC KEY-----', '-----BEGIN PRIVATE KEY-----
MIIEvwIBADANBgkqhkiG9w0BAQEFAASCBKkwggSlAgEAAoIBAQClIODUxkpaF0Kv
pr6a489ZFJPaKGqSAbVlCzLjHs61XzLw04OoVOQsVfRT0fOV5xLykZvgVFd+Fuxi
IG5CnCNfcDfDhubq/RzBgoCzpncQphfJbQg4S8bAxPYCARGOJspIr/l7l9YJtXqF
fhY+u4d3s2hbh5eZnkqq4eWylVSxjKQE6TA7U28bX8F2LIizWWiMf/DR3uXMe/1f
fcr3jvXTid7+mxlIETo7gRH0cs2fY4L6T9jqL2T0zy1z2iXz0OOot/YDdiXiSv0p
z58uEVxsMv8j9kZHY7nCNZnaSW5zOu6i8S72pVspCUZd2yAX9KMlJfs28TH8Vt5b
KH4f3dQTAgMBAAECggEANLz6cZpe9sUFDFqB97AYcgfrbLqJxtX63Cmmd/lH5Y9h
Ep9LJkDumuZc4URlupORbp1onk3xn5isqG5ZKLb7aSjK9oyoRZbPT1Jhzn60+kFB
yZgkDu7MrJZ7R4qV08O5yVtCn+GPf4xRdD8f1atJCcbn5ou4tFxk1ozNMqsU4Jta
EsKnSDbQyKXYPh63Etk9N/YFcqJmue+qW3/BHeaAGhS/ffp242FONfyDbyTQ1hP2
ZhLFoqqp+6SONNOKv31DhznknJGhYCyBpSmt6AH20bYg6/cXGopwKuqPhJMZNJfT
7kGMQt8ztTswaEnP/RTQ/OM3m/o9txalx6zXaud5MQKBgQDQh3Yu6xMPGpkj+vSE
xyYXH9NI4gb1GRt7aclc2WXeHZu3z0MS+4U9gLss5vYMDc6rOn+uXLBR0w4TM8I0
VxOuEq14d5PenmHkIW8/zJ2FGxx0CJPFVmjuxvwzQO4Xv3IB+h36QE6NqEjWZeYO
6kReQJGPL6YqjBUtUC+g4rqiWwKBgQDKuCKjDaqikaDMnmUL11sHu3lB/H3rC8aB
/pU44chMk6Mo7M9AMEqn8WxJqpMs2Fm55M8OigTrThiaQHapyx4c1mTBM2eB9m5E
d8T06qU7CY0ykWw56P+m68e8ZT8jMMUENvbEL144PBDI9xxWHJ3vO6KXW/4ELl2X
YQT70Y7SqQKBgQCvmAHqzERGSa1+4MmaLWBjxIbULpCRP00nBRNsvhvLh78ibsMa
bvXprkbvLqUtzUqkYSQ3HRHUNRb7EHCORmzp/gxZDshTg6cGxSi63oAeH5A8HSTh
3TuLJcAlIJ3dHXbRN16a5h+/pV+AOloZYSek/lYT7pnL5h3HBGi233UtVQKBgQCI
jKoALSeAZBntWtBzvnd56OVP9nUXC5KGY+c+LpAOTPB9k64y7ZSMNcocgQKsFXuz
BRYYFA4mNi/KT5ve2YwKHrw1i5uwZj92bTrFQv1TnCZ5Jo3BIFp8pJuhIo359VLw
eNmV4tZTiNpBlHqI4rPbvs7Rp8yeImE2o8qImWrcKQKBgQCAbSAVDhyQNgm+zRIc
4pEFFY/IdlPj+1L9WhjsfB25WwjcWIWwLJpDuyPx3xeLMTN0t4aZvOMBTBps5Fgp
B1Q6obt4M4bvqAxk9CXA6+WqP2rcXVXop8mn8nGMh9K18dZlMH0ylGs8JqOMqX0S
GfRoB71aiGhuvfj9qqOkOq7syg==
-----END PRIVATE KEY-----', 'Application', '2024-01-01 00:00:00', '2024-01-01 00:00:00');
INSERT INTO accounts (id, username, domain, display_name, note, public_key, private_key, actor_type, discoverable, indexable, fields,
  avatar_file_name, avatar_content_type, avatar_file_size, created_at, updated_at) VALUES
 (109000000000000001, 'alice', NULL, 'Alice :blobcat:', '<p>Hello from Mastodon</p>', '-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAixaKPTsBAsiOpSfgdpsw
8X7HyFxbftfzw3hWajXdsQc89L+sffnC27CESvJSXFvif/IPB4IvdzPIXOtV5yl0
ZmFsh+ZY1xGlE5K4NDvtm0vvob2wNIEwoTNb8ryJuUTYKcUkmZ87keuPmulyLU58
7USWdxLjuBccdcBaEw25CSrB0qAkp0Tt3MHsNMHCy0JRULoggJ+XQ7Nns35NKBDf
KWeJKG9SzgBtl06VQrN66A4BPjXsW2TDNPQJ/RLznqO5GgqsNfwaHUgPbhGd5qLL
pw2UtC2JEC1HVWMHwalz/WP4iI1kjG3I1Xm3knOkTNhzIA0PvjTpS8aHvDEuo5WN
bwIDAQAB
-----END PUBLIC KEY-----
', '-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQCLFoo9OwECyI6l
J+B2mzDxfsfIXFt+1/PDeFZqNd2xBzz0v6x9+cLbsIRK8lJcW+J/8g8Hgi93M8hc
61XnKXRmYWyH5ljXEaUTkrg0O+2bS++hvbA0gTChM1vyvIm5RNgpxSSZnzuR64+a
6XItTnztRJZ3EuO4Fxx1wFoTDbkJKsHSoCSnRO3cwew0wcLLQlFQuiCAn5dDs2ez
fk0oEN8pZ4kob1LOAG2XTpVCs3roDgE+NexbZMM09An9EvOeo7kaCqw1/BodSA9u
EZ3mosunDZS0LYkQLUdVYwfBqXP9Y/iIjWSMbcjVebeSc6RM2HMgDQ++NOlLxoe8
MS6jlY1vAgMBAAECggEANyCabxVtW6SRQS2SfoAZQIZ++c99ZkOCtuWB3rv1394e
D2XPIOk3DesjLC9qbHfwyMRwp4m2dXkdP5L5K9OW8qNHqH+ok6nHM4BSKUXdcdGB
q6oq5kyUMThRh96pebirMrcUQB1IN5hVhVFks0dqJYKrXouqnULvwU4ZhXbJcbOe
oltTOPSTXfCGlEFW7BBsmjuQBD1dcIaDLZaVgiZRvllbtZlypoYmfAQoTN7SYYcs
+jhK0YA+UttW9SISs/efjFPSNCw2ooFObD0EDZiR195GFZ3x1786NhaGLpznXLxH
K0sHSROpWOG+jmVdH//g+fFiksr/l6RbPpCm2UopAQKBgQC+7cLCM6Z/HqLinrjT
RWbwZ/sjC2L/kGzpihmfmOHavFf8F/fsC0OWrFRzVTdbnA24reDbF/XyXPR2KGwa
zgvU/41JSP/blHmUYWObn/HkUWAYH7PtsEbQWbCyA+DBzl54bNPhkvC44fdVv/qI
LCMnhbKdp73M/RQh6lwvbtlubwKBgQC6fcHvt32ILe1mfFFkHl3Rj2ttonCZFMVS
CvZGv/6S7JbSQT6OxOXgo30/Bo0L5yFPGVcIgQOUmfOqADFaG6JlkyvDa5+mPbVs
hazyUa7+BwZyfnewwQywhCINtOI3iUk84PMlPQnJ9RrgPxIrajcHFG8bgENB3rrO
OBfVGWVRAQKBgDE6mNb9/lYUjickib9fy7T8+pIEo5gFoy9sPkMaZlHB8mW7wF8K
YHj/Ih9Cv61NRXRy0OFtAitlsFqu/YDD0+Utpz5YJjB1zgtmOQqtaO9X1BEkS35j
zz51gcvEmDUggtdGKDOncY4aJW3kvDJUPxQ3suBXmea68m/0P6SzTjjdAoGAE3VH
pPFqQak5GfZir6WTeGgUd235fZ9zLypBpAQoVcAlxG5WTqMwsQ2d5dOTJ+jHyu8D
67k6XGjryZMXkyVK/noqmMtD7x6FLXuNHgNz1MF65LtZVbHu1wQSYYe8CrtiTt3R
wNLrkIzobvllte3xK+Xv6diqK9dHenjT08JAlgECgYEAo7cCiXP3ps6GYNdnmaG8
nhv0O48743qhqJDLwuL6C+N0SsV/ehD0F5FP7CBUBS3zaifg1xy8EHLiqmuiLAHP
nM9jo2iw432S596dopa8ww6ga2HoH0Fa5KDSrZxX86rc1A/mvVEZvAjSGAnFTMK5
qZTVsNoyjCUYzGez7lWEGEg=
-----END PRIVATE KEY-----
', 'Person', true, true,
  '[{"name":"Site","value":"https://alice.example"}]', 'avatar.png', 'image/png', 67, '2024-01-02 03:04:05', '2024-01-02 03:04:05'),
 (109000000000000002, 'bob', NULL, 'Bob', '', '-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAx5GYcdSHErThcWAs40za
NCg5R8kULLLRKGdEyZtctRkZMrMakORW0IEwx7k6nA56xPkeYEw7RBnSreByzaef
DwU5BpBaOtD7cvHG9l9pg9ALj2eGF8WXEtDl3ZJoxR0CcZLT9tZj8mI5CaOLXpjC
ldeSUsQnXIqe5m0NoLyBH+FY6VIBy7bp3RjslMOUxRGSKNbWzbDYdp81zkaIoMc+
Bxb+FiSIJ6lIXILwqBLDraDOlHfW0I7c8LIs/6ktsAnVWOt2H9B4B89ZOkLUVzGq
6DlOX5FLZWpJQOYBLhUCbArdra0B2B2ha3WOxA0J5dJGX/LPkkdYdYQLrV2FJSIq
sQIDAQAB
-----END PUBLIC KEY-----', '-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDHkZhx1IcStOFx
YCzjTNo0KDlHyRQsstEoZ0TJm1y1GRkysxqQ5FbQgTDHuTqcDnrE+R5gTDtEGdKt
4HLNp58PBTkGkFo60Pty8cb2X2mD0AuPZ4YXxZcS0OXdkmjFHQJxktP21mPyYjkJ
o4temMKV15JSxCdcip7mbQ2gvIEf4VjpUgHLtundGOyUw5TFEZIo1tbNsNh2nzXO
Roigxz4HFv4WJIgnqUhcgvCoEsOtoM6Ud9bQjtzwsiz/qS2wCdVY63Yf0HgHz1k6
QtRXMaroOU5fkUtlaklA5gEuFQJsCt2trQHYHaFrdY7EDQnl0kZf8s+SR1h1hAut
XYUlIiqxAgMBAAECggEABBsC5PLQU6iiucTPoHrICmMWyMzULhZV36MiASLeMEOQ
I5Y0G0L3SOiUzlG0E6FZg+GexFsNHfYz0AE91EIjoRkH9qCa7+wm5zasU5r4qPHA
uVnY969WekosF4Yl+9gctlAWu8ZYbAYXCy/xAdMvku4DQ+5mcckll7MOLyj/0bNf
/NOQqdtObayGrWOXU+2VpMTCjCD9WK5nYPgROlw1Drx4PZ9v4Yxna18ZvEMJtgHw
nYmxigaLa0QUV0jzhQgzXeYixGYU9T2FJ4ANHpUUxdvAsG4hrecADj0oK983DPPA
x2JAyT9G8XHjNdNUPf88TAy+2OGMoxhAYZt2yulPoQKBgQDy5TEHWnkLXf1+wglQ
YPC1MY36AGc0JutUFTXRSyqnrpp4kQnnoWHPhf8oJhxrvQls4ZrsDQ9gq3XPWpUI
Brx778Fgmuv4H9fr1Z35ybeXHWvdJb8AO/U0+rlQ7fJtD0XPF+WZwstxPeHtMW/l
khfGqWj62ZTEvfQVAp1UqjpmzQKBgQDSVf0VF9XNmdi8UqfRdSDxySFpvB4cgR5p
9vHVdxh1CwVYRNEsrfkkZ7y/eOmFcGIjpNZs59rGfHA23H062L2j3e3p1GV8nfMB
mdWkAAqtVPoxLjsLUFEi+d2prwPGPDYF7hvvHozC9zKgPBhXAVXkJbyMYoshn+7V
hzWV7l/rdQKBgE9wefqHsnLvSIr+dwFc0xzPmjmbjCVYXTrgvPs4dDjnOAoalXy2
IUE3991vqXfrGXwMxRhSpSberOcmHvvDCD4YicxInshF0d6FvOcG/A5mRvLK947i
DS5ImfdH+zOD/Tbf/+2r8dGVhaCHvKr+ftwwZ+8y3h8CaSO9+SrJidQ5AoGAS2b0
EUm4JbqPTgO7kYURyFu4FLWRfdpz0rzqOQI2zE6lHd2dtwyvEfP2YBWycVm2dNy0
2dnMPfeCKsikRCJK6aG3lmnD8AaXhO1m/yHcsgKSpmrSU935GDbG1fuduMUBAOM5
NGGZSJNhnLuuZhJefilhDFqPUgCJmBaeiR8pV7kCgYEAlYjL+BHem8koEHlhymtM
ZFSEFU5bSQZu5xlXEmwiUFpB/RmLdqtUvAnBsM2m7oErZE9AFNgcD3qW5rqSn3EF
XNhvim5ILaiFzB1bxTvXBS75WwCPybTe8S/Hy1iuwJdO5eOhLUdmDc/EmEk4IXpb
W+/2sBCZdnRLRAvLM8gvHXo=
-----END PRIVATE KEY-----', 'Person', true, false, '[]', NULL, NULL, NULL, '2024-01-03 03:04:05', '2024-01-03 03:04:05');
INSERT INTO accounts (id, username, domain, display_name, uri, url, inbox_url, shared_inbox_url, public_key, actor_type, created_at, updated_at) VALUES
 (109000000000000003, 'carol', 'remote.example', 'Carol', 'https://remote.example/users/carol', 'https://remote.example/@carol', 'https://remote.example/users/carol/inbox',
  'https://remote.example/inbox', '-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAkllPYb7LT3FgQ7RcGjLn
yPb3qBaHVygHW90fnwlrLeCQ/QzFUzQ0+V2PQxWwi/kZbyToFeW7HiZbNX970z9y
alNWDhQYVMi02QB3qNulJFnPGN02ue07JbRDmv9vZTgZnfdFe+t/3RTAnigsfkYO
7K+qugIrtN64pJe8pqYOffebd8wBZJHRqQ2m6yzRtY/FslnBXLf2cFOXdnMTWBaq
LhbudpHuguHm1ipQJduJsLgVdGEpKhD1sqIHF18/QD/4eH4AQSK1CyyubYbq/IQM
kPHHV6OdIkFxvJgoyKxmdpGgW0IVEq2GEb0zY3rMEyVZSHhADVQaLcpH1dZNRK/v
VwIDAQAB
-----END PUBLIC KEY-----', 'Person', '2024-01-04 00:00:00', '2024-01-04 00:00:00');
INSERT INTO users (id, account_id, email, encrypted_password, confirmed_at, role_id, locale) VALUES
 (1, 109000000000000001, 'alice@example.org', '$2a$10$TYjteQMoj7uggFJvh901FufpBtU0Ppbs68I4W7.Z961iysNFiTnYy', '2024-01-02 03:04:05', 3, 'en'),
 (2, 109000000000000002, 'bob@example.org', '$2a$10$TYjteQMoj7uggFJvh901FufpBtU0Ppbs68I4W7.Z961iysNFiTnYy', '2024-01-03 03:04:05', NULL, 'de');
INSERT INTO statuses (id, uri, text, created_at, updated_at, url, visibility, language, local, account_id, ordered_media_attachment_ids) VALUES
 (110000000000000001, 'http://localhost:18083/users/alice/statuses/110000000000000001', 'Hello fediverse :blobcat: #intro', '2024-02-01 10:00:00', '2024-02-01 10:00:00', 'http://localhost:18083/@alice/110000000000000001', 0, 'en', true, 109000000000000001, '{111000000000000001}'),
 (110000000000000002, 'http://localhost:18083/users/alice/statuses/110000000000000002', 'Followers only', '2024-02-02 10:00:00', '2024-02-02 10:00:00', 'http://localhost:18083/@alice/110000000000000002', 2, 'en', true, 109000000000000001, NULL),
 (110000000000000003, 'https://remote.example/users/carol/statuses/9', '<p>Hi <a href="http://localhost:18083/@alice">@alice</a></p>', '2024-02-03 10:00:00', '2024-02-03 10:00:00', 'https://remote.example/@carol/9', 0, 'en', false, 109000000000000003, NULL),
 (110000000000000004, 'http://localhost:18083/users/bob/statuses/110000000000000004', 'Which one?', '2024-02-04 10:00:00', '2024-02-04 10:00:00', 'http://localhost:18083/@bob/110000000000000004', 0, 'en', true, 109000000000000002, NULL);
INSERT INTO media_attachments (id, status_id, account_id, file_file_name, file_content_type, file_file_size, description, file_meta, created_at) VALUES
 (111000000000000001, 110000000000000001, 109000000000000001, 'photo.png', 'image/png', 67, 'A tiny picture', '{"original":{"width":1,"height":1},"focus":{"x":0.1,"y":-0.2}}', '2024-02-01 09:59:00');
INSERT INTO mentions VALUES (1, 110000000000000003, 109000000000000001, false);
INSERT INTO tags VALUES (1, 'intro');
INSERT INTO statuses_tags VALUES (110000000000000001, 1);
INSERT INTO status_pins VALUES (1, 109000000000000001, 110000000000000001);
INSERT INTO follows (id, account_id, target_account_id, uri, created_at) VALUES
 (1, 109000000000000003, 109000000000000001, 'https://remote.example/users/carol#follows/1', '2024-02-05 00:00:00'),
 (2, 109000000000000002, 109000000000000001, 'http://localhost:18083/users/bob#follows/2', '2024-02-05 00:00:00');
INSERT INTO favourites VALUES (1, 109000000000000002, 110000000000000001, '2024-02-06 00:00:00');
INSERT INTO bookmarks VALUES (1, 109000000000000002, 110000000000000001);
INSERT INTO polls VALUES (1, 109000000000000002, 110000000000000004, '2030-01-01 00:00:00', '{Tea,Coffee}', '{1,0}', false);
INSERT INTO poll_votes VALUES (1, 109000000000000001, 1, 0, '2024-02-04 11:00:00');
INSERT INTO custom_emoji_categories VALUES (1, 'Blobs');
INSERT INTO custom_emojis (id, shortcode, domain, image_file_name, image_content_type, image_file_size, category_id) VALUES
 (1, 'blobcat', NULL, 'blobcat.png', 'image/png', 67, 1);
