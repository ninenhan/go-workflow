package core

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

func (r *UnitRepository) RegisterUnit(name string, unit any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Mappings == nil {
		r.Mappings = make(map[string]reflect.Type)
	}
	r.Mappings[name] = reflect.TypeOf(unit).Elem()
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

func ParsePhaseUnits(jsonData []byte, typeField string) ([]Unit, error) {
	return getUnitRepo().ParsePhaseUnits(jsonData, typeField)
}

func ParsePhaseUnitsFromMap(rawList []map[string]any) ([]Unit, error) {
	return getUnitRepo().ParsePhaseUnitsFromMap(rawList)
}
