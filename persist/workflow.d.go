package persist

import "gorm.io/gorm"

type DashBoardOrm struct {
	gorm.Model

	Name        string `gorm:"type:varchar(128);not null"` // 标签名称
	DashBoardID string `gorm:"type:varchar(64);index"`     // 所属行业 ID
}
