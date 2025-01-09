package core

import "errors"

// 故事版跟Pipeline的相似度处：
// 1. 都有Stage[]
// 2. 都有Stage关系
// 从StoryBoard创建Pipeline，只要有连线，校验通过
type StoryBoard struct {
	Stages []*Stage //这里不是真正的Stage ， 需要套一层UI-Data ，StageVo -》 Stage
	Lines  []*Line
}

type Line struct {
	From string
	To   string
}

func (u *StoryBoard) findStage(lineId string) *Stage {
	for _, stage := range u.Stages {
		if stage.Name == lineId {
			return stage
		}
	}
	return nil
}
func (u *StoryBoard) Build() (p *Pipeline, e error) {
	// 校验
	if len(u.Stages) == 0 {
		return
	}
	if len(u.Lines) == 0 && len(u.Stages) != 1 {
		return
	}
	if len(u.Lines) == 0 {
		p = NewPipeline(nil, u.Stages[0])
		return
	}
	var re []*Stage

	for _, line := range u.Lines {
		if line.From == "" || line.To == "" {
			continue
		}
		fromStage := u.findStage(line.From)
		if fromStage == nil {
			if len(re) > 0 {
				e = errors.New("storyboard stage line error")
				return
			}
			continue
		}
		re = append(re, fromStage)
		toStage := u.findStage(line.To)
		if toStage == nil {
			return nil, errors.New("storyboard stage line end error")
		}
		re = append(re, toStage)
	}
	p = NewPipeline(nil, re...)
	return
}
