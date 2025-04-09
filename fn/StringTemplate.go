package fn

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	//symbolRawPrefix  = "{%"
	//symbolRawSuffix  = "%}"
	symbolAnnoPrefix = "{#"
	symbolAnnoSuffix = "#}"
	symbolPrefix     = "{{"
	symbolSuffix     = "}}"
)

type TemplateNeedle struct {
	Template     string `json:"template"`
	DefaultValue string `json:"defaultValue"`
}

// ParseTemplate 从模板文本中提取所有占位符，返回键值对：键为参数名称，值为占位符文本
func ParseTemplate(templateText string) (map[string]TemplateNeedle, error) {
	model := make(map[string]TemplateNeedle)
	// 利用正则匹配形如 {{...}} 的占位符
	re := regexp.MustCompile(regexp.QuoteMeta(symbolPrefix) + `(.*?)` + regexp.QuoteMeta(symbolSuffix))
	matches := re.FindAllStringSubmatch(templateText, -1)
	for _, match := range matches {
		// match[1] 为括号中的内容
		if len(match) >= 2 {
			paramName := strings.TrimSpace(match[1])
			parts := strings.Split(paramName, ":")
			placeholder := symbolPrefix + paramName + symbolSuffix
			defaultVal := ""
			if len(parts) > 1 {
				defaultVal = strings.Join(parts[1:], ":")
			}
			model[parts[0]] = TemplateNeedle{
				Template:     placeholder,
				DefaultValue: defaultVal,
			}
		}
	}
	return model, nil
}

// CheckModelValid 检查模型中每个键是否合法：非空且只允许英文、数字、下划线以及中文字符
func CheckModelValid(model map[string]any) error {
	// 正则：允许 a-z, A-Z, 0-9, 下划线, 以及中文字符（\p{Han}）
	re := regexp.MustCompile(`^[a-zA-Z0-9_\\$\p{Han}]+$`)
	for key := range model {
		if strings.TrimSpace(key) == "" {
			return errors.New("检测到模板参数，但缺少真实值")
		}
		if !re.MatchString(key) {
			return fmt.Errorf("模板参数“%s”不合法，只允许英文、数字、下划线和中文", key)
		}
	}
	return nil
}

var pathSegmentRegex = regexp.MustCompile(`([\p{Han}\\$\w]+)($begin:math:display$\\d+$end:math:display$)*`)

// ParsePathExpr 将路径字符串（如 用户.好友[0].昵称）解析为 ["用户", "好友", 0, "昵称"]
func ParsePathExpr(expr string) []any {
	var result []any
	segments := strings.Split(expr, ".")

	for _, seg := range segments {
		matches := pathSegmentRegex.FindStringSubmatch(seg)
		if len(matches) == 0 {
			continue
		}
		result = append(result, matches[1]) // 字段名
		// 查找所有索引
		indexMatches := regexp.MustCompile(`$begin:math:display$(\\d+)$end:math:display$`).FindAllStringSubmatch(seg, -1)
		for _, im := range indexMatches {
			idx, _ := strconv.Atoi(im[1])
			result = append(result, idx)
		}
	}
	return result
}

// GetByPath 从嵌套结构中通过解析后的路径获取值
func GetByPath(data any, path []any) any {
	current := data
	for _, p := range path {
		switch key := p.(type) {
		case string:
			m, ok := current.(map[string]any)
			if !ok {
				return nil
			}
			current = m[key]
		case int:
			arr, ok := current.([]any)
			if !ok || key >= len(arr) {
				return nil
			}
			current = arr[key]
		default:
			return nil
		}
	}
	return current
}

// GetValue 从 map 中通过路径表达式获取值（支持中文、嵌套、数组）
func GetValue(data map[string]any, expr string) any {
	path := ParsePathExpr(expr)
	return GetByPath(data, path)
}

// RenderTemplateStrictly 根据传入的模型，将模板中的占位符替换为实际值
func RenderTemplateStrictly(templateText string, slots map[string]TemplateNeedle, model map[string]any, strict bool) string {
	//替换掉 {# --- #}
	// 提前编译正则表达式，并使用非贪婪匹配
	var annoRegex = regexp.MustCompile(
		regexp.QuoteMeta(symbolAnnoPrefix) + `.*?` + regexp.QuoteMeta(symbolAnnoSuffix),
	)
	templateText = annoRegex.ReplaceAllString(templateText, "")
	// 简单实现：遍历每个 key，将对应占位符替换
	result := templateText
	for key, val := range slots {
		value := model[key]
		if strings.Contains(key, ".") {
			//从model依次访问path
			value = GetValue(model, key)
		}
		placeholder := val.Template
		placeholder = strings.ReplaceAll(placeholder, symbolPrefix, fmt.Sprintf("%s\\s*", symbolPrefix))
		placeholder = strings.ReplaceAll(placeholder, symbolSuffix, fmt.Sprintf("\\s*%s", symbolSuffix))
		str := fmt.Sprint(value)
		fin := Ternary(IsDataEmpty(value), val.DefaultValue, str)
		if !IsDataEmpty(fin) {
			result = RegexReplace(result, placeholder, fin)
		} else if strict {
			result = RegexReplace(result, placeholder, "")
		}
	}
	return result
}
func ParseTemplateTest() {
	// 示例模板文本
	inputText := `
	{# 文章名称:描述的社会背景1 #} XXX {# 文章名称:描述的社会背景3 #}
这是一段文章《{{文章名称 }}》，请你帮我提炼出{{主旨名称:描述的社会背景}}，并且告诉我{{人物}}的相关信息。
{# 文章名称:描述的社会背景2 #}
`
	// 解析模板中的占位符
	parsed, err := ParseTemplate(inputText)
	if err != nil {
		fmt.Println("解析模板出错：", err)
		return
	}
	fmt.Println("提取到的占位符：", parsed)

	// 检查模型参数是否合法（这里只是示例模型）
	// 准备渲染模板的数据（只替换部分占位符）
	renderModel := map[string]any{
		"文章名称": "汤姆叔叔的小屋",
		"人物":   "主人公和发生地点",
	}
	if err := CheckModelValid(renderModel); err != nil {
		fmt.Println("模型参数不合法：", err)
	} else {
		fmt.Println("模型参数合法")
	}
	rendered := RenderTemplateStrictly(inputText, parsed, renderModel, true)
	fmt.Println("渲染后的模板：", rendered)
}
