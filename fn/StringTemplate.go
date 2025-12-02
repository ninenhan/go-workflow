package fn

import (
	"errors"
	"fmt"
	"github.com/Knetic/govaluate"
	"github.com/ninenhan/go-profile/utils"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	//symbolRawPrefix  = "{%"
	//symbolRawSuffix  = "%}"
	symbolAnnoPrefix = "{#"
	symbolAnnoSuffix = "#}"
	symbolPrefix     = "{{"
	symbolSuffix     = "}}"

	tagStart  = "<%"
	tagEnd    = "%>"
	echoStart = "<%="
)

type Token struct {
	Kind string // text | if | else | end
	Val  string
}

func tokenize(input string) []Token {
	var tokens []Token
	for {
		startEcho := strings.Index(input, echoStart)
		startTag := strings.Index(input, tagStart)

		// 选择最近出现的那一个
		start := -1
		isEcho := false

		if startEcho >= 0 && (startTag < 0 || startEcho < startTag) {
			start = startEcho
			isEcho = true
		} else {
			start = startTag
		}
		if isEcho {
			// echo 分支
			end := strings.Index(input[start:], tagEnd)
			cmd := input[start+3 : start+end] // 跳过 "<%="
			tokens = append(tokens, Token{"echo", strings.TrimSpace(cmd)})
			input = input[start+end+2:]
			continue
		}
		if start < 0 {
			if len(input) > 0 {
				tokens = append(tokens, Token{Kind: "text", Val: input})
			}
			break
		}
		if start > 0 {
			tokens = append(tokens, Token{Kind: "text", Val: input[:start]})
		}
		end := strings.Index(input[start:], tagEnd)
		if end < 0 {
			break
		}
		cmd := strings.TrimSpace(input[start+2 : start+end])
		switch {
		case strings.HasPrefix(cmd, "if "):
			tokens = append(tokens, Token{Kind: "if", Val: strings.TrimSpace(cmd[3:])})

		case strings.HasPrefix(cmd, "elseif "):
			tokens = append(tokens, Token{Kind: "elseif", Val: strings.TrimSpace(cmd[7:])})

		case cmd == "else":
			tokens = append(tokens, Token{Kind: "else"})

		case cmd == "end":
			tokens = append(tokens, Token{Kind: "end"})
		case strings.HasPrefix(cmd, "="):
			tokens = append(tokens, Token{Kind: "echo", Val: strings.TrimSpace(cmd[1:])})
		default:
			// 也作为文本
			tokens = append(tokens, Token{Kind: "text", Val: "<%" + cmd + "%>"})
		}
		input = input[start+end+2:]
	}
	return tokens
}
func parseNodes(tokens []Token, i int) ([]Node, int, error) {
	var nodes []Node

	for i < len(tokens) {
		tk := tokens[i]

		switch tk.Kind {

		case "text":
			nodes = append(nodes, TextNode{Text: tk.Val})
			i++
		case "echo":
			nodes = append(nodes, EchoNode{Expr: tk.Val})
			i++
		case "if":
			ifNode := &IfNode{
				Conds:  []string{tk.Val},
				Blocks: [][]Node{},
			}

			// 解析 if 第一段
			block, ni, err := parseNodes(tokens, i+1)
			if err != nil {
				return nil, 0, err
			}
			ifNode.Blocks = append(ifNode.Blocks, block)
			i = ni

			// elseif 解析
			for i < len(tokens) && tokens[i].Kind == "elseif" {
				cond := tokens[i].Val
				i++

				bk, ni2, err := parseNodes(tokens, i)
				if err != nil {
					return nil, 0, err
				}

				ifNode.Conds = append(ifNode.Conds, cond)
				ifNode.Blocks = append(ifNode.Blocks, bk)
				i = ni2
			}

			// else 分支
			if i < len(tokens) && tokens[i].Kind == "else" {
				bk, ni3, err := parseNodes(tokens, i+1)
				if err != nil {
					return nil, 0, err
				}
				ifNode.Else = bk
				i = ni3
			}

			// end
			if i >= len(tokens) || tokens[i].Kind != "end" {
				return nil, 0, errors.New("missing <% end %>")
			}
			i++ // consume end

			nodes = append(nodes, ifNode)

		case "else", "elseif", "end":
			return nodes, i, nil
		}
	}
	return nodes, i, nil
}

var evalFunctions map[string]govaluate.ExpressionFunction

func evalExprValue(expr string, model map[string]any) any {
	e, err := govaluate.NewEvaluableExpressionWithFunctions(expr, evalFunctions)
	if err != nil {
		return ""
	}
	res, err := e.Evaluate(model)
	if err != nil {
		return ""
	}
	return res
}

func evalExpr(expr string, model map[string]any) bool {
	e, err := govaluate.NewEvaluableExpressionWithFunctions(expr, evalFunctions)
	if err != nil {
		return false
	}
	res, err := e.Evaluate(model)
	if err != nil {
		return false
	}
	b, ok := res.(bool)
	return ok && b
}

func RenderControlNodes(nodes []Node, model map[string]any) string {
	var sb strings.Builder
	for _, n := range nodes {
		switch v := n.(type) {
		case TextNode:
			sb.WriteString(v.Text)
		case EchoNode:
			val := evalExprValue(v.Expr, model)
			sb.WriteString(fmt.Sprint(val))
		case *IfNode:
			matched := false
			// if + elseif
			for idx, cond := range v.Conds {
				if evalExpr(cond, model) {
					sb.WriteString(RenderControlNodes(v.Blocks[idx], model))
					matched = true
					break
				}
			}

			// else
			if !matched {
				sb.WriteString(RenderControlNodes(v.Else, model))
			}
		}
	}
	return sb.String()
}

func ParseControlBlocks(input string) ([]Node, error) {
	tokens := tokenize(input)
	nodes, _, err := parseNodes(tokens, 0)
	return nodes, err
}

// -------- 控制流 AST --------
type Node interface{}

type TextNode struct {
	Text string
}

type EchoNode struct {
	Expr string
}

type IfNode struct {
	Conds  []string // if + elseif 共用
	Blocks [][]Node // 每个条件对应一个 block
	Else   []Node   // else 内容
}

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
	//re := regexp.MustCompile(`^[a-zA-Z0-9_\\$\p{Han}]+$`)
	re := regexp.MustCompile(`^[\p{Han}a-zA-Z0-9_]+$`)
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
		fin := utils.Ternary(IsDataEmpty(value), val.DefaultValue, str)
		if !IsDataEmpty(fin) {
			result = RegexReplace(result, placeholder, fin)
		} else if strict {
			result = RegexReplace(result, placeholder, "")
		}
	}
	return result
}

func DefaultTemplateRender(inputText string, sourceMap map[string]any, result *string) error {
	// 解析模板中的占位符
	parsed, err := ParseTemplate(inputText)
	if err != nil {
		fmt.Println("解析模板出错：", err)
		return errors.New("解析出错")
	}
	if err := CheckModelValid(sourceMap); err != nil {
		fmt.Println("模型参数不合法：", err)
	}
	*result = RenderTemplateStrictly(inputText, parsed, sourceMap, true)
	return nil
}

func RenderTemplateWithControl(input string, model map[string]any) (string, error) {
	// 解析控制块 AST
	nodes, err := ParseControlBlocks(input)
	if err != nil {
		return "", err
	}
	// 执行控制流
	step1 := RenderControlNodes(nodes, model)
	// slot 渲染
	var final string
	err = DefaultTemplateRender(step1, model, &final)
	return final, err
}

func init() {
	evalFunctions = map[string]govaluate.ExpressionFunction{}

	evalFunctions["len"] = func(args ...any) (any, error) {
		if len(args) == 0 {
			return float64(0), nil
		}
		switch v := args[0].(type) {
		case string:
			return float64(utf8.RuneCountInString(v)), nil
		case []any:
			return float64(len(v)), nil
		case map[string]any:
			return float64(len(v)), nil
		default:
			return float64(0), nil
		}
	}

	evalFunctions["number"] = func(args ...any) (any, error) {
		if len(args) == 0 {
			return float64(0), nil
		}

		switch v := args[0].(type) {

		case nil:
			return float64(0), nil

		case int:
			return float64(v), nil
		case int8:
			return float64(v), nil
		case int16:
			return float64(v), nil
		case int32:
			return float64(v), nil
		case int64:
			return float64(v), nil

		case uint:
			return float64(v), nil
		case uint8:
			return float64(v), nil
		case uint16:
			return float64(v), nil
		case uint32:
			return float64(v), nil
		case uint64:
			return float64(v), nil

		case float32:
			return float64(v), nil
		case float64:
			return v, nil

		case string:
			// 尝试解析字符串成数字
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f, nil
			}
			return float64(0), nil

		default:
			// 其他类型一律返回 0
			return float64(0), nil
		}
	}

	evalFunctions["empty"] = func(args ...any) (any, error) {
		if len(args) == 0 {
			return true, nil
		}
		switch v := args[0].(type) {
		case nil:
			return true, nil
		case string:
			return strings.TrimSpace(v) == "", nil
		case []any:
			return len(v) == 0, nil
		case map[string]any:
			return len(v) == 0, nil
		default:
			return false, nil
		}
	}

	evalFunctions["notempty"] = func(args ...any) (any, error) {
		r, _ := evalFunctions["empty"](args...)
		b, _ := r.(bool)
		return !b, nil
	}

	evalFunctions["contains"] = func(args ...any) (any, error) {
		if len(args) < 2 {
			return false, nil
		}
		s, _ := args[0].(string)
		sub, _ := args[1].(string)
		return strings.Contains(s, sub), nil
	}

	evalFunctions["starts"] = func(args ...any) (any, error) {
		if len(args) < 2 {
			return false, nil
		}
		s, _ := args[0].(string)
		p, _ := args[1].(string)
		return strings.HasPrefix(s, p), nil
	}

	evalFunctions["ends"] = func(args ...any) (any, error) {
		if len(args) < 2 {
			return false, nil
		}
		s, _ := args[0].(string)
		p, _ := args[1].(string)
		return strings.HasSuffix(s, p), nil
	}
}

func ParseTemplateTest() {
	// 示例模板文本
	inputText := `
		<%= len(人物) %>
		<% if contains(文章名称, "1汤姆叔叔") %>
		你是成年人。
		<% elseif len(title) == 8 %>
		刚好 18  
		<% else %>
		你还未成年。
		<% end %>
	{# 文章名称:描述的社会背景1 #} XXX {# 文章名称:描述的社会背景3 #}
这是一段文章《{{文章名称 }}》，请你帮我提炼出{{主旨名称:描述的社会背景}}，{# 文章名称:描述的社会背景2 #}
并且告诉我{{人物}}的相关信息。
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
		"人物":     "主人公和发生地点",
		"age":      18,
		"title":    "主人公和发生地点",
	}
	if err := CheckModelValid(renderModel); err != nil {
		fmt.Println("模型参数不合法：", err)
	} else {
		fmt.Println("模型参数合法")
	}
	rendered, _ := RenderTemplateWithControl(inputText, renderModel)
	//rendered := RenderTemplateStrictly(inputText, parsed, renderModel, true)
	fmt.Println("渲染后的模板：", rendered)
}
