package flow

import (
	"errors"
)

// StoryBoard 故事版跟Pipeline的相似度处：
// 1. 都有Units[]
// 2. 都有Units关系
// 从StoryBoard创建Pipeline，只要有连线，校验通过
type StoryBoard struct {
	Units []PhaseUnit //这里不是真正的Stage ， 需要套一层UI-Data ，StageVo -》 Stage
	Lines []Line
}

type Line struct {
	From string
	To   string
}

func (u *StoryBoard) findUnit(from string) *PhaseUnit {
	//u.Units
	for _, unit := range u.Units {
		if unit.GetID() == from {
			return &unit
		}
	}
	return nil
}

func (u *StoryBoard) Build() (p *Pipeline, e error) {
	// 校验
	if len(u.Lines) == 0 && len(u.Units) != 1 {
		return
	}
	if len(u.Lines) == 0 {
		p = NewPipeline(u.Units)
		return
	}
	var re []PhaseUnit

	for _, line := range u.Lines {
		if line.From == "" || line.To == "" {
			continue
		}
		fromUnit := u.findUnit(line.From)
		if fromUnit == nil {
			if len(re) > 0 {
				e = errors.New("storyboard stage line error")
				return
			}
			continue
		}
		re = append(re, *fromUnit)
		toStage := u.findUnit(line.To)
		if toStage == nil {
			return nil, errors.New("storyboard stage line end error")
		}
		re = append(re, *toStage)
	}
	p = NewPipeline(re)
	return
}
