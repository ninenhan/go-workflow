package service

import (
	"gorm.io/gorm"
)

type PersistService struct {
	Orm *gorm.DB
}

func NewPersistService(orm *gorm.DB) *PersistService {
	//数据集合初始化，定义集合名称、唯一索引等
	//if err := orm.Table(tagTableName).AutoMigrate(&model.Tag{}); err != nil {
	//	log.Fatal("migration failed:", err)
	//}
	//if err := orm.Table(tagSynonymTableName).AutoMigrate(&model.TagSynonym{}); err != nil {
	//	log.Fatal("migration failed:", err)
	//}
	return &PersistService{Orm: orm}
}
