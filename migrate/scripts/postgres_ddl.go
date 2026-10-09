package scripts

import (
	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// ddlFor picks the hand-written DDL for conn's engine. The MySQL text is
// the historical script and is used unchanged for MySQL/MariaDB (and any
// other engine that ran it before); PostgreSQL gets an equivalent script.
//
// PostgreSQL differences handled by the *PostgresSQL variants:
//   - BIGSERIAL instead of BIGINT UNSIGNED AUTO_INCREMENT, SMALLINT for
//     TINYINT, TIMESTAMPTZ for DATETIME, no ENGINE/CHARSET/COMMENT clauses;
//   - no ON UPDATE CURRENT_TIMESTAMP (GORM's autoUpdateTime sets updated_at);
//   - index names are unique per schema, not per table, so every index is
//     created separately with a table-prefixed name.
//
// execTableDDL creates the tables of a hand-written DDL script. SQLite
// cannot run either the MySQL or the PostgreSQL text (COMMENT, ENGINE,
// BIGSERIAL, ...), so on SQLite the same tables are created from their GORM
// models instead; a fresh SQLite install used to stop at the first of these
// scripts.
func execTableDDL(conn *gorm.DB, mysqlSQL, postgresSQL string, sqliteModels ...any) error {
	if dialect.IsSQLite(conn) {
		return conn.AutoMigrate(sqliteModels...)
	}
	return conn.Exec(ddlFor(conn, mysqlSQL, postgresSQL)).Error
}

func ddlFor(conn *gorm.DB, mysqlSQL, postgresSQL string) string {
	if dialect.IsPostgres(conn) {
		return postgresSQL
	}

	return mysqlSQL
}

const groupTablesPostgresSQL = `
CREATE TABLE IF NOT EXISTS "groups" (
	id BIGSERIAL PRIMARY KEY,
	owner_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	name VARCHAR(255) NOT NULL DEFAULT '',
	description TEXT DEFAULT '',
	avatar VARCHAR(512) DEFAULT '',
	type SMALLINT NOT NULL DEFAULT 0,
	visibility VARCHAR(20) NOT NULL DEFAULT 'private',
	matrix_room_id VARCHAR(255) DEFAULT NULL,
	created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS idx_groups_owner_id ON "groups" (owner_id);
CREATE INDEX IF NOT EXISTS idx_groups_type ON "groups" (type);
CREATE INDEX IF NOT EXISTS idx_groups_visibility ON "groups" (visibility);
CREATE INDEX IF NOT EXISTS idx_groups_matrix_room_id ON "groups" (matrix_room_id);
CREATE INDEX IF NOT EXISTS idx_groups_deleted_at ON "groups" (deleted_at);

CREATE TABLE IF NOT EXISTS group_members (
	id BIGSERIAL PRIMARY KEY,
	group_id BIGINT NOT NULL REFERENCES "groups"(id) ON DELETE CASCADE,
	account_id BIGINT DEFAULT NULL REFERENCES accounts(id) ON DELETE SET NULL,
	matrix_user_id VARCHAR(255) DEFAULT NULL,
	display_name VARCHAR(255) DEFAULT '',
	role SMALLINT NOT NULL DEFAULT 0,
	invited_by BIGINT DEFAULT NULL REFERENCES accounts(id) ON DELETE SET NULL,
	matrix_invited_by VARCHAR(255) DEFAULT NULL,
	joined_at TIMESTAMPTZ DEFAULT NULL,
	created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS idx_group_members_group_id ON group_members (group_id);
CREATE INDEX IF NOT EXISTS idx_group_members_account_id ON group_members (account_id);
CREATE INDEX IF NOT EXISTS idx_group_members_matrix_user_id ON group_members (matrix_user_id);
CREATE INDEX IF NOT EXISTS idx_group_members_role ON group_members (role);
CREATE INDEX IF NOT EXISTS idx_group_members_deleted_at ON group_members (deleted_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_members_group_account ON group_members (group_id, account_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_members_group_matrix ON group_members (group_id, matrix_user_id);

CREATE TABLE IF NOT EXISTS group_messages (
	id BIGSERIAL PRIMARY KEY,
	group_id BIGINT NOT NULL REFERENCES "groups"(id) ON DELETE CASCADE,
	sender_id BIGINT DEFAULT NULL REFERENCES accounts(id) ON DELETE SET NULL,
	matrix_sender_id VARCHAR(255) DEFAULT NULL,
	content TEXT NOT NULL,
	matrix_event_id VARCHAR(255) DEFAULT NULL,
	created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS idx_group_messages_group_id ON group_messages (group_id);
CREATE INDEX IF NOT EXISTS idx_group_messages_sender_id ON group_messages (sender_id);
CREATE INDEX IF NOT EXISTS idx_group_messages_matrix_sender_id ON group_messages (matrix_sender_id);
CREATE INDEX IF NOT EXISTS idx_group_messages_created_at ON group_messages (created_at);
CREATE INDEX IF NOT EXISTS idx_group_messages_deleted_at ON group_messages (deleted_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_messages_matrix_event ON group_messages (matrix_event_id);

CREATE TABLE IF NOT EXISTS contacts (
	id BIGSERIAL PRIMARY KEY,
	account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	contact_type SMALLINT NOT NULL DEFAULT 0,
	contact_account_id BIGINT DEFAULT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	contact_matrix_id VARCHAR(255) DEFAULT NULL,
	display_name VARCHAR(255) DEFAULT '',
	status SMALLINT NOT NULL DEFAULT 0,
	notes TEXT DEFAULT '',
	created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS idx_contacts_account_id ON contacts (account_id);
CREATE INDEX IF NOT EXISTS idx_contacts_contact_type ON contacts (contact_type);
CREATE INDEX IF NOT EXISTS idx_contacts_contact_account_id ON contacts (contact_account_id);
CREATE INDEX IF NOT EXISTS idx_contacts_contact_matrix_id ON contacts (contact_matrix_id);
CREATE INDEX IF NOT EXISTS idx_contacts_status ON contacts (status);
CREATE INDEX IF NOT EXISTS idx_contacts_deleted_at ON contacts (deleted_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_contacts_account_contact ON contacts (account_id, contact_account_id, contact_matrix_id);

CREATE TABLE IF NOT EXISTS contact_requests (
	id BIGSERIAL PRIMARY KEY,
	from_account_id BIGINT DEFAULT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	from_matrix_user_id VARCHAR(255) DEFAULT NULL,
	to_account_id BIGINT DEFAULT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	to_matrix_user_id VARCHAR(255) DEFAULT NULL,
	message TEXT DEFAULT '',
	status SMALLINT NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS idx_contact_requests_from_account_id ON contact_requests (from_account_id);
CREATE INDEX IF NOT EXISTS idx_contact_requests_from_matrix_user_id ON contact_requests (from_matrix_user_id);
CREATE INDEX IF NOT EXISTS idx_contact_requests_to_account_id ON contact_requests (to_account_id);
CREATE INDEX IF NOT EXISTS idx_contact_requests_to_matrix_user_id ON contact_requests (to_matrix_user_id);
CREATE INDEX IF NOT EXISTS idx_contact_requests_status ON contact_requests (status);
CREATE INDEX IF NOT EXISTS idx_contact_requests_deleted_at ON contact_requests (deleted_at);
`

const matrixTablesPostgresSQL = `
CREATE TABLE IF NOT EXISTS matrix_users (
	id BIGSERIAL PRIMARY KEY,
	local_account_id BIGINT NOT NULL,
	matrix_user_id VARCHAR(255) NOT NULL,
	matrix_localpart VARCHAR(128) NOT NULL,
	matrix_server VARCHAR(128) NOT NULL,
	direction VARCHAR(16) NOT NULL DEFAULT 'local',
	display_name VARCHAR(128) DEFAULT '',
	avatar_url VARCHAR(512) DEFAULT '',
	created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS idx_matrix_users_local_account_id ON matrix_users (local_account_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_matrix_users_matrix_user_id ON matrix_users (matrix_user_id);
CREATE INDEX IF NOT EXISTS idx_matrix_users_matrix_localpart ON matrix_users (matrix_localpart);
CREATE INDEX IF NOT EXISTS idx_matrix_users_matrix_server ON matrix_users (matrix_server);
CREATE INDEX IF NOT EXISTS idx_matrix_users_direction ON matrix_users (direction);
CREATE INDEX IF NOT EXISTS idx_matrix_users_deleted_at ON matrix_users (deleted_at);

CREATE TABLE IF NOT EXISTS matrix_rooms (
	id BIGSERIAL PRIMARY KEY,
	conversation_id BIGINT NOT NULL,
	matrix_room_id VARCHAR(255) NOT NULL,
	matrix_room_localpart VARCHAR(128) DEFAULT '',
	matrix_server VARCHAR(128) NOT NULL,
	is_direct BOOLEAN NOT NULL DEFAULT TRUE,
	room_name VARCHAR(255) DEFAULT '',
	last_event_id VARCHAR(255) DEFAULT '',
	last_sync_at TIMESTAMPTZ DEFAULT NULL,
	created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ DEFAULT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_matrix_rooms_conversation_id ON matrix_rooms (conversation_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_matrix_rooms_matrix_room_id ON matrix_rooms (matrix_room_id);
CREATE INDEX IF NOT EXISTS idx_matrix_rooms_matrix_room_localpart ON matrix_rooms (matrix_room_localpart);
CREATE INDEX IF NOT EXISTS idx_matrix_rooms_matrix_server ON matrix_rooms (matrix_server);
CREATE INDEX IF NOT EXISTS idx_matrix_rooms_is_direct ON matrix_rooms (is_direct);
CREATE INDEX IF NOT EXISTS idx_matrix_rooms_deleted_at ON matrix_rooms (deleted_at);

CREATE TABLE IF NOT EXISTS matrix_messages (
	id BIGSERIAL PRIMARY KEY,
	status_id BIGINT NOT NULL,
	matrix_event_id VARCHAR(255) NOT NULL,
	matrix_room_id VARCHAR(255) NOT NULL,
	matrix_sender_id VARCHAR(255) NOT NULL,
	direction VARCHAR(16) NOT NULL,
	created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_matrix_messages_status_id ON matrix_messages (status_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_matrix_messages_matrix_event_id ON matrix_messages (matrix_event_id);
CREATE INDEX IF NOT EXISTS idx_matrix_messages_matrix_room_id ON matrix_messages (matrix_room_id);
CREATE INDEX IF NOT EXISTS idx_matrix_messages_matrix_sender_id ON matrix_messages (matrix_sender_id);
CREATE INDEX IF NOT EXISTS idx_matrix_messages_direction ON matrix_messages (direction);
CREATE INDEX IF NOT EXISTS idx_matrix_messages_created_at ON matrix_messages (created_at);
`

const relayTablePostgresSQL = `
CREATE TABLE IF NOT EXISTS relays (
	id BIGSERIAL PRIMARY KEY,
	inbox_url VARCHAR(512) NOT NULL,
	follow_activity_id VARCHAR(255),
	state SMALLINT NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	deleted_at TIMESTAMPTZ DEFAULT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_relays_inbox_url ON relays (inbox_url);
CREATE INDEX IF NOT EXISTS idx_relays_state ON relays (state);
CREATE INDEX IF NOT EXISTS idx_relays_follow_activity_id ON relays (follow_activity_id);
CREATE INDEX IF NOT EXISTS idx_relays_deleted_at ON relays (deleted_at);
`
