package db

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
	"time"
)

type FileInfo struct {
	Id       string `gorm:"primaryKey"`
	Name     string
	FileSize int64
	IsDir    bool
	ModTime  time.Time
	Content  []byte
	ParentId *string
}

func (fi *FileInfo) BeforeCreate(tx *gorm.DB) (err error) {
	if fi.Id == "" {
		fi.Id = uuid.New().String()
	}
	return nil
}
