package models

type SystemSetting struct {
	ID  int64  `gorm:"primary_key;autoIncrement"           json:"id"`
	Key string `gorm:"unique;not null;type:varchar(64)"    json:"key"`
	Val any    `gorm:"type:json;not null; serializer:json" json:"val"`
}

func (SystemSetting) TableName() string {
	return "system_settings"
}

// Settings naming imported system actors. When set, the account whose
// local_actor_uri equals the value is the instance actor (relay actor), so a
// migrated server keeps signing and being reachable as the source's actor
// (e.g. Misskey's https://<domain>/users/<id>, keyId <uri>#main-key) instead
// of a newly generated <base>/actor identity.
const (
	SettingInstanceActorURI = "federation.instance_actor_uri"
	SettingRelayActorURI    = "federation.relay_actor_uri"
)
