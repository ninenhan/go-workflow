package workflow

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

type UnitRepository struct {
	mu       sync.RWMutex
	Mappings map[string]reflect.Type // 存储所有单元的映射关系
}

// region //  deprecated: 使用 RegisterUnit 和 FindUnit 方法
func (r *UnitRepository) RegisterUnit(name string, unit any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Mappings == nil {
		r.Mappings = make(map[string]reflect.Type)
	}
	r.Mappings[name] = reflect.TypeOf(unit).Elem()
}

func (r *UnitRepository) FindUnit(name string) (*Unit, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.Mappings == nil {
		return nil, fmt.Errorf("unit repository is not initialized")
	}
	unitType, ok := r.Mappings[name]
	if !ok || unitType == nil {
		return nil, fmt.Errorf("unit %s not found", name)
	}
	unitPtr := reflect.New(unitType).Interface()
	u, ok := unitPtr.(Unit)
	if !ok {
		return nil, fmt.Errorf("unit %s is not a valid Unit type", name)
	}
	return &u, nil
}

func (r *UnitRepository) ParsePhaseUnitsFromMap(rawList []map[string]any) ([]Unit, error) {
	data, err := json.Marshal(rawList)
	if err != nil {
		return nil, err
	}
	return r.ParsePhaseUnits(data, "")
}

func (r *UnitRepository) ParsePhaseUnits(jsonData []byte, typeField string) ([]Unit, error) {
	var rawUnits []map[string]any
	if err := json.Unmarshal(jsonData, &rawUnits); err != nil {
		return nil, err
	}
	if typeField == "" {
		typeField = "unit_name"
	}
	var units []Unit
	for _, raw := range rawUnits {
		typeVal, ok := raw[typeField].(string)
		if !ok {
			return nil, fmt.Errorf("缺少 {%s} 字段", typeField)
		}

		unitType, ok := r.Mappings[typeVal]
		if !ok {
			return nil, fmt.Errorf("未知的类型: %s", typeVal)
		}

		unitPtr := reflect.New(unitType).Interface()
		unitJSON, _ := json.Marshal(raw)

		if err := json.Unmarshal(unitJSON, unitPtr); err != nil {
			return nil, err
		}

		units = append(units, unitPtr.(Unit))
	}
	return units, nil
}

// 获取或初始化全局 unitRepo
//func getUnitRepo() *UnitRepository {
//	unitRepoOnce.Do(func() {
//		unitRepo = &UnitRepository{
//			Mappings: make(map[string]reflect.Type),
//		}
//	})
//	return unitRepo
//}

// endregion
var (
	mu sync.RWMutex
)
var UnitRegistry = map[string]ExecutableUnit{}

func RegisterUnit(name string, unit ExecutableUnit) {
	UnitRegistry[name] = unit
}

func FindUnit(name string) (ExecutableUnit, bool) {
	mu.RLock()
	defer mu.RUnlock()
	u, e := UnitRegistry[name]
	return u, e
}
