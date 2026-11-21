package models

import "time"

// BookmarkFolder is a user-defined bookmark collection that can be private or
// publicly shared (inspired by openBshi/app/service/main/favorite).
type BookmarkFolder struct {
	Id          uint64     `gorm:"primary_key;autoIncrement" json:"id,string"`
	AccountId   uint64     `gorm:"not null;index"            json:"account_id,string"`
	Title       string     `gorm:"type:varchar(64);not null" json:"title"`
	Description string     `gorm:"type:varchar(256);not null;default:''" json:"description"`
	IsPublic    bool       `gorm:"not null;default:false"    json:"is_public"`
	ItemsCount  uint64     `gorm:"not null;default:0"        json:"items_count"`
	CreatedAt   *time.Time `gorm:"not null;autoCreateTime"   json:"created_at"`
	UpdatedAt   *time.Time `gorm:"not null;autoUpdateTime"   json:"updated_at"`
}

func (BookmarkFolder) TableName() string {
	return "bookmark_folders"
}

// BookmarkFolderItem links a status to a BookmarkFolder.
type BookmarkFolderItem struct {
	Id        uint64     `gorm:"primary_key;autoIncrement"                                  json:"id"`
	FolderId  uint64     `gorm:"not null;uniqueIndex:idx_bookmark_folder_status,priority:1" json:"folder_id"`
	AccountId uint64     `gorm:"not null;index"                                             json:"account_id"`
	StatusId  uint64     `gorm:"not null;uniqueIndex:idx_bookmark_folder_status,priority:2;index" json:"status_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"                                    json:"created_at"`
}

func (BookmarkFolderItem) TableName() string {
	return "bookmark_folder_items"
}
