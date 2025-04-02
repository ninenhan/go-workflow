package flow

import (
	"reflect"
	"sync"
)

var (
	unitRepoOnce sync.Once
	// Registry registry 保存了所有已注册的 unit 类型，key 为类型标识
	unitRepo *UnitRepository
)

// 获取或初始化全局 unitRepo
func getUnitRepo() *UnitRepository {
	unitRepoOnce.Do(func() {
		unitRepo = &UnitRepository{
			Mappings: make(map[string]reflect.Type),
		}
	})
	return unitRepo
}

func RegisterUnit(name string, unit any) {
	getUnitRepo().RegisterUnit(name, unit)
}

func ParsePhaseUnits(jsonData []byte, typeField string) ([]PhaseUnit, error) {
	return getUnitRepo().ParsePhaseUnits(jsonData, typeField)
}

func ParsePhaseUnitsFromMap(rawList []map[string]any) ([]PhaseUnit, error) {
	return getUnitRepo().ParsePhaseUnitsFromMap(rawList)
}
