package biographyskills

import _ "embed"

type Definition struct {
	Identifier  string
	Title       string
	Description string
	Content     string
	Priority    int32
}

//go:embed conduct-biography-interview/SKILL.md
var conductBiographyInterview string

//go:embed verify-biography-facts/SKILL.md
var verifyBiographyFacts string

//go:embed structure-biography-chapters/SKILL.md
var structureBiographyChapters string

func BiographyDefinitions() []Definition {
	return []Definition{
		{
			Identifier:  "conduct-biography-interview",
			Title:       "专业自传采访",
			Description: "围绕成书目标像专业传记记者一样自然追问，在场景、感受、关系、选择与人生意义之间灵活取舍。",
			Content:     conductBiographyInterview,
			Priority:    100,
		},
		{
			Identifier:  "verify-biography-facts",
			Title:       "自传事实核验",
			Description: "温和确认时间、地点、人物关系和冲突记忆，避免把推测或模糊回忆写成确定事实。",
			Content:     verifyBiographyFacts,
			Priority:    90,
		},
		{
			Identifier:  "structure-biography-chapters",
			Title:       "自传章节梳理",
			Description: "按成书目标持续整理章节叙事维度，判断继续追问、回看旧章节、确认事实或转入下一阶段。",
			Content:     structureBiographyChapters,
			Priority:    80,
		},
	}
}
