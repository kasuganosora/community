-- Minimal Misskey (2026.x) schema fixture for the setup wizard migration
-- tests: only the tables and columns the exporter reads. Keys and the
-- password hash are test-only values. Load into an empty PostgreSQL database.
-- Source site: http://localhost:18082 (local file URLs reveal it to the wizard).
CREATE TABLE "user" (id varchar(32) PRIMARY KEY, username varchar(128) NOT NULL, "usernameLower" varchar(128), host varchar(128),
  name varchar(128), "avatarId" varchar(32), "bannerId" varchar(32), "avatarUrl" varchar(512), "bannerUrl" varchar(512),
  "isLocked" boolean DEFAULT false, "isBot" boolean DEFAULT false, "isExplorable" boolean DEFAULT true, "isSuspended" boolean DEFAULT false,
  "isDeleted" boolean DEFAULT false, emojis varchar(128)[] DEFAULT '{}', uri varchar(512), url varchar(512), inbox varchar(512), "sharedInbox" varchar(512),
  "createdAt" timestamptz, "updatedAt" timestamptz);
CREATE TABLE user_profile ("userId" varchar(32) PRIMARY KEY, description varchar(2048), fields jsonb DEFAULT '[]', email varchar(128),
  "emailVerified" boolean DEFAULT false, password varchar(128), "twoFactorEnabled" boolean DEFAULT false, "usePasswordLessLogin" boolean DEFAULT false,
  "securityKeysAvailable" boolean DEFAULT false, "followingVisibility" varchar(32) DEFAULT 'public', "followersVisibility" varchar(32) DEFAULT 'public', "noCrawle" boolean DEFAULT false);
CREATE TABLE user_keypair ("userId" varchar(32) PRIMARY KEY, "publicKey" varchar(4096), "privateKey" varchar(4096));
CREATE TABLE user_publickey ("userId" varchar(32) PRIMARY KEY, "keyId" varchar(256), "keyPem" varchar(4096));
CREATE TABLE note (id varchar(32) PRIMARY KEY, "userId" varchar(32) NOT NULL, "userHost" varchar(128), text text, cw varchar(512),
  visibility varchar(16) NOT NULL, "localOnly" boolean DEFAULT false, "visibleUserIds" varchar(32)[] DEFAULT '{}', "replyId" varchar(32), "renoteId" varchar(32),
  "threadId" varchar(256), "fileIds" varchar(32)[] DEFAULT '{}', tags varchar(128)[] DEFAULT '{}', emojis varchar(128)[] DEFAULT '{}', "hasPoll" boolean DEFAULT false,
  "channelId" varchar(32), uri varchar(512), url varchar(512), "createdAt" timestamptz);
CREATE TABLE drive_file (id varchar(32) PRIMARY KEY, "userId" varchar(32), "userHost" varchar(128), name varchar(256), type varchar(128), size integer,
  url varchar(512), "accessKey" varchar(256), "thumbnailUrl" varchar(512), "webpublicUrl" varchar(512), "thumbnailAccessKey" varchar(256), "webpublicAccessKey" varchar(256),
  "storedInternal" boolean, "isLink" boolean DEFAULT false, "isSensitive" boolean DEFAULT false, comment varchar(512), blurhash varchar(128), properties jsonb DEFAULT '{}', "createdAt" timestamptz);
CREATE TABLE emoji (id varchar(32) PRIMARY KEY, name varchar(128) NOT NULL, host varchar(128), "originalUrl" varchar(512), "publicUrl" varchar(512),
  category varchar(128), aliases varchar(128)[] DEFAULT '{}', type varchar(64), license varchar(1024), "isSensitive" boolean DEFAULT false, "updatedAt" timestamptz);
CREATE TABLE following (id varchar(32) PRIMARY KEY, "followerId" varchar(32) NOT NULL, "followeeId" varchar(32) NOT NULL, "followerHost" varchar(128),
  "followeeHost" varchar(128), "followerInbox" varchar(512), "followerSharedInbox" varchar(512), notify varchar(32), "createdAt" timestamptz);
CREATE TABLE meta (id varchar(32) PRIMARY KEY, name varchar(1024), description varchar(1024), "maintainerEmail" varchar(1024), "disableRegistration" boolean DEFAULT false,
  "useObjectStorage" boolean DEFAULT false, "objectStorageBaseUrl" varchar(1024), "objectStorageBucket" varchar(1024), "objectStorageEndpoint" varchar(1024),
  "objectStorageRegion" varchar(1024), "objectStorageAccessKey" varchar(1024), "objectStorageSecretKey" varchar(1024), "objectStorageUseSSL" boolean DEFAULT true,
  "objectStorageS3ForcePathStyle" boolean DEFAULT true, "blockedHosts" varchar(1024)[] DEFAULT '{}', "serverRules" varchar(280)[] DEFAULT '{}');
CREATE TABLE user_security_key (id varchar(256) PRIMARY KEY, "userId" varchar(32) NOT NULL, "publicKey" varchar(1024), name varchar(30), counter bigint DEFAULT 0,
  "credentialDeviceType" varchar(32), "credentialBackedUp" boolean, transports varchar(32)[], "lastUsed" timestamptz);

INSERT INTO meta (id, name, description, "maintainerEmail", "blockedHosts", "serverRules") VALUES
 ('x', 'Fixture Misskey', 'A migrated test instance', 'ops@example.org', '{spam.example}', '{"Be kind"}');
INSERT INTO "user" (id, username, "usernameLower", host, name, "avatarId", "createdAt", emojis) VALUES
 ('9xk2a1b2c3d4e5f6', 'alice', 'alice', NULL, 'Alice :blobcat:', '9xk2c1b2c3d4e5f1', '2024-01-02T03:04:05Z', '{blobcat}'),
 ('9xk2a1b2c3d4e5f7', 'bob', 'bob', NULL, 'Bob', NULL, '2024-01-03T03:04:05Z', '{}');
INSERT INTO "user" (id, username, "usernameLower", host, name, uri, url, inbox, "sharedInbox", "createdAt") VALUES
 ('9xk2a1b2c3d4e5f8', 'carol', 'carol', 'remote.example', 'Carol', 'https://remote.example/users/carol', 'https://remote.example/@carol', 'https://remote.example/users/carol/inbox', 'https://remote.example/inbox', '2024-01-04T00:00:00Z');
INSERT INTO user_profile ("userId", description, fields, email, "emailVerified", password) VALUES
 ('9xk2a1b2c3d4e5f6', 'Hello from Misskey', '[{"name":"Site","value":"https://alice.example"}]', 'alice@example.org', true, '$2b$10$8qQdqswic2c.96lDYcmCEeMjN.7l/ED/3EtY7O9oO3ggrmzFJbfZ6'),
 ('9xk2a1b2c3d4e5f7', '', '[]', 'bob@example.org', true, '$2b$10$8qQdqswic2c.96lDYcmCEeMjN.7l/ED/3EtY7O9oO3ggrmzFJbfZ6'),
 ('9xk2a1b2c3d4e5f8', NULL, '[]', NULL, false, NULL);
INSERT INTO user_keypair ("userId", "publicKey", "privateKey") VALUES ('9xk2a1b2c3d4e5f6', '-----BEGIN PUBLIC KEY-----
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
'), ('9xk2a1b2c3d4e5f7', '-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAonf0B9QsE0BCDBIKCaAr
9Os2Cg9c/sNz7RwCvfsmVWU086wZQ0DxIWqkJshZkrYaAiUmondQnZ6qRHKOegQb
qh0UjBevvW7c5Ws3IkQzyEqB/TBcldkYyAOl6QWhlRkeUmZ4dxgXqvtzJtcyEbAH
V8d7+5nzttCnVBA2jhfVH7C6VTYiRwsCUdaCs8BYcZ6oFJ/8GbuJdvk3K6Qk6+os
4t9JkPdsaJifkbE/pBcE0KMiUYZiDOSvaWhMWFMFYtWQ8iLfy65WsKKDemp+co4X
eM0uDl5jBMVw9HbHaTdNdnRFM99111q/3j0OwjxyQha122pLAm3Ty/Zx02jtKBsU
ZQIDAQAB
-----END PUBLIC KEY-----
', '-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQCid/QH1CwTQEIM
EgoJoCv06zYKD1z+w3PtHAK9+yZVZTTzrBlDQPEhaqQmyFmSthoCJSaid1CdnqpE
co56BBuqHRSMF6+9btzlazciRDPISoH9MFyV2RjIA6XpBaGVGR5SZnh3GBeq+3Mm
1zIRsAdXx3v7mfO20KdUEDaOF9UfsLpVNiJHCwJR1oKzwFhxnqgUn/wZu4l2+Tcr
pCTr6izi30mQ92xomJ+RsT+kFwTQoyJRhmIM5K9paExYUwVi1ZDyIt/LrlawooN6
an5yjhd4zS4OXmMExXD0dsdpN012dEUz33XXWr/ePQ7CPHJCFrXbaksCbdPL9nHT
aO0oGxRlAgMBAAECggEAANqx0GS5aQFNsTPBevStQBfVUrVrK/Yp768JBXS2A+O8
JPF2HiUDEsW1YHRtooum+vUbc3+ktd9157w7RMFgX9zQe3GyCkRaw8rausLL2IH2
/JzUSWY5CIkBLO0YDc+lZpHq9woll0/3kAm4b3oseU98Ns6qOf9CeGFJcb/TiA7r
FHcUmD3Ltdu7BZVDv/C4olEsWa1/NUWrB8WHvzj2smpkPgq8pSa1GcW95L4XzehK
hYuZf+DtoEUkWRjq6OZMK8sQ3ovca34GI7nwOi3GS7Dl6e8B/2D6Ix5rQ7cQ2xXY
AxMGwtZJGZTj+vwKzbHBQH3ZGhX+LE6ZYuODpo5qwQKBgQDf4W4/PvyW1wbycUaV
i0M/WtGGk4Gwd7qCQ1rrH7MkfEQWWGMFWkkhK/9CpwIL5vgWQTLf6vgdJRC9vWiC
Guf7xj2mg8f6svh+TlVEVOf4VeSW1kIWngEJGrBsofQHSgzjxmgfEcLM0YEuQiBn
7zLW44HMIuIfANIb5/ToQbE6FQKBgQC5xwOSHU9iYFVfcRCeMoZd/+4OX4+DcGej
WH3iBe3+BAiCr3Ui8RNt/ozxfEZW9l/+XhdNCDMRc3cKyYVTbREwXjxkggutckPs
ZYZUU7reAqv3n243UDPAxxYnRU8IjMHK1I0BStVb5V4nBOvo8NwJR1K71tuY72pl
KHGv3GiVEQKBgQCVeKGtkjT71EYsGZrBfv//zXe0PMlKczVPgP6zmi91fY3LyWkb
CsNmZUSAUJZaMfOSkCcSPoM2mieKNmoO6z3U5+8DPFqIs3KaiVv+Sh2xvE7osdXf
2c/hLzN448vUDgl7DV042RfQvVNc+gM+wQGVXMGs8+E6R8b/oKbvZeSYXQKBgDfQ
rSf3GmSqILw+EgoVj/0p6XH0EAAjg1+2NIv10B0jRRNOd9j5qSbA9cOP3Oo/t3OK
/WEtd3pkwH1DMoVwHVv01Bw2Xh/MVs/taMBpL5mIH/aTtd90fzgKGIPP9QDSmV7B
IWQkAZpMfU8n0WD5DILVJOw+j1eRhtvBnPaDOAChAoGBAKl6m9Ec/axrFiRaxtWY
lFpjFVkFSsGIpdIc98oRyKREVfU3iNxgT/IioZyvbF433FnScLajnNw5zoTVqvu4
GYFPFUafwgraYZ/vxU6zlHWIZ5SQstMwL6jV+Zp3D6wf+6YabgLEPo91CIhvnfW+
8GW3JUyx+KFilb9T8QHo5cfh
-----END PRIVATE KEY-----
');
INSERT INTO user_publickey ("userId", "keyId", "keyPem") VALUES ('9xk2a1b2c3d4e5f8', 'https://remote.example/users/carol#main-key', '-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAr3mEVqmu4g/QJIY5cnlx
yMvdxb2LdFEKq3tMP/bwcGwL/GOJ7TSMAS8LIRL4pAYJdMII95SMAwCNFR/+s3x/
BeuZFJIE3hrj073i5wTQAzCp9DBFBlTNxT3ZHKN97COpH6Y2lonb8Mz85evNMIJS
GKZVfL2hlnd4pq64ak9Ew1+OeFsNWMmjc3Q8bV9wtamSRGxzQkXLrjlAwWoc1kaN
Uxr3NJ4MpBl7Z91tLg38lPCCkL6R4mA8CPCQ2+Tkv8vYGJsat7qPAH0MKLgZcJyl
XklMUFwk3YzLRusyIIMRedsKH/Z14jIzXeO1Zb/qYUbX5ev4009FrT1RfVNi/mY0
ZQIDAQAB
-----END PUBLIC KEY-----
');
INSERT INTO drive_file (id, "userId", "userHost", name, type, size, url, "accessKey", "storedInternal", properties, "createdAt") VALUES
 ('9xk2c1b2c3d4e5f1', '9xk2a1b2c3d4e5f6', NULL, 'avatar.png', 'image/png', 67, 'http://localhost:18082/files/avatar-key.png', 'avatar-key.png', true, '{"width":1,"height":1}', '2024-01-02T03:05:00Z');
INSERT INTO emoji (id, name, host, "originalUrl", "publicUrl", category, type) VALUES
 ('9xk2d1b2c3d4e5f1', 'blobcat', NULL, 'http://localhost:18082/files/blobcat-key.png', 'http://localhost:18082/files/blobcat-key.png', 'cats', 'image/png');
INSERT INTO note (id, "userId", text, visibility, "createdAt", "fileIds", emojis) VALUES
 ('9xk2b1b2c3d4e5f1', '9xk2a1b2c3d4e5f6', 'First note :blobcat:', 'public', '2024-02-01T00:00:00Z', '{9xk2c1b2c3d4e5f1}', '{blobcat}'),
 ('9xk2b1b2c3d4e5f3', '9xk2a1b2c3d4e5f6', 'Followers only', 'followers', '2024-02-03T00:00:00Z', '{}', '{}');
INSERT INTO note (id, "userId", text, visibility, "replyId", "threadId", "createdAt") VALUES
 ('9xk2b1b2c3d4e5f2', '9xk2a1b2c3d4e5f7', 'Reply from bob', 'public', '9xk2b1b2c3d4e5f1', '9xk2b1b2c3d4e5f1', '2024-02-02T00:00:00Z');
INSERT INTO note (id, "userId", "userHost", text, visibility, uri, url, "createdAt") VALUES
 ('9xk2b1b2c3d4e5f4', '9xk2a1b2c3d4e5f8', 'remote.example', 'Remote note', 'public', 'https://remote.example/notes/1', 'https://remote.example/notes/1', '2024-02-04T00:00:00Z');
INSERT INTO following (id, "followerId", "followeeId", "followerHost", "followeeHost", "followerInbox", "followerSharedInbox", "createdAt") VALUES
 ('9xk2e1b2c3d4e5f1', '9xk2a1b2c3d4e5f7', '9xk2a1b2c3d4e5f6', NULL, NULL, NULL, NULL, '2024-01-05T00:00:00Z'),
 ('9xk2e1b2c3d4e5f2', '9xk2a1b2c3d4e5f8', '9xk2a1b2c3d4e5f6', 'remote.example', NULL, 'https://remote.example/users/carol/inbox', 'https://remote.example/inbox', '2024-01-06T00:00:00Z'),
 ('9xk2e1b2c3d4e5f3', '9xk2a1b2c3d4e5f6', '9xk2a1b2c3d4e5f8', NULL, 'remote.example', NULL, NULL, '2024-01-07T00:00:00Z');
