package workflow

import (
	"fmt"
	"sort"
	"strings"
)

// WorkflowToDOT renders a WorkflowDefinition to Graphviz DOT.
func WorkflowToDOT(def *WorkflowDefinition) string {
	if def == nil {
		return "digraph workflow {}"
	}
	var b strings.Builder
	b.WriteString("digraph workflow {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")

	nodeIDs := make([]string, 0, len(def.Nodes))
	for id := range def.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	for _, id := range nodeIDs {
		spec := def.Nodes[id]
		label := id
		if spec != nil {
			name := strings.TrimSpace(spec.Name)
			unit := strings.TrimSpace(spec.Unit)
			if unit == "" {
				unit = strings.TrimSpace(spec.UnitID)
			}
			if name != "" {
				label = name
			}
			if unit != "" {
				label = fmt.Sprintf("%s\\n[%s]", label, unit)
			}
		}
		b.WriteString(fmt.Sprintf("  %q [label=%q];\n", id, label))
	}

	edges := append([]EdgeSpec{}, def.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].When < edges[j].When
	})
	for _, e := range edges {
		label := strings.TrimSpace(e.Label)
		if label == "" {
			label = strings.TrimSpace(e.When)
		}
		if label != "" {
			b.WriteString(fmt.Sprintf("  %q -> %q [label=%q];\n", e.From, e.To, label))
		} else {
			b.WriteString(fmt.Sprintf("  %q -> %q;\n", e.From, e.To))
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// WorkflowToMermaid renders a WorkflowDefinition to Mermaid flowchart.
func WorkflowToMermaid(def *WorkflowDefinition) string {
	if def == nil {
		return "flowchart LR\n"
	}
	var b strings.Builder
	b.WriteString("flowchart LR\n")

	nodeIDs := make([]string, 0, len(def.Nodes))
	for id := range def.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	for _, id := range nodeIDs {
		spec := def.Nodes[id]
		label := id
		if spec != nil {
			name := strings.TrimSpace(spec.Name)
			unit := strings.TrimSpace(spec.Unit)
			if unit == "" {
				unit = strings.TrimSpace(spec.UnitID)
			}
			if name != "" {
				label = name
			}
			if unit != "" {
				label = fmt.Sprintf("%s<br/>[%s]", label, unit)
			}
		}
		b.WriteString(fmt.Sprintf("  %s[%q]\n", sanitizeMermaidID(id), label))
	}

	edges := append([]EdgeSpec{}, def.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].When < edges[j].When
	})
	for _, e := range edges {
		from := sanitizeMermaidID(e.From)
		to := sanitizeMermaidID(e.To)
		label := strings.TrimSpace(e.Label)
		if label == "" {
			label = strings.TrimSpace(e.When)
		}
		if label != "" {
			b.WriteString(fmt.Sprintf("  %s -->|%s| %s\n", from, escapeMermaidLabel(label), to))
		} else {
			b.WriteString(fmt.Sprintf("  %s --> %s\n", from, to))
		}
	}
	return b.String()
}

// GraphToDOT renders an in-memory Graph to Graphviz DOT.
func GraphToDOT(g *Graph) string {
	if g == nil {
		return "digraph workflow {}"
	}
	def := graphToDefinition(g)
	return WorkflowToDOT(def)
}

// GraphToMermaid renders an in-memory Graph to Mermaid flowchart.
func GraphToMermaid(g *Graph) string {
	if g == nil {
		return "flowchart LR\n"
	}
	def := graphToDefinition(g)
	return WorkflowToMermaid(def)
}

func graphToDefinition(g *Graph) *WorkflowDefinition {
	def := &WorkflowDefinition{
		Nodes: map[string]*NodeSpec{},
	}
	for id, n := range g.Nodes {
		if n == nil {
			continue
		}
		def.Nodes[id] = &NodeSpec{
			ID:   id,
			Name: n.Name,
		}
	}
	for from, tos := range g.Edges {
		for _, to := range tos {
			def.Edges = append(def.Edges, EdgeSpec{From: from, To: to})
		}
	}
	return def
}

func sanitizeMermaidID(id string) string {
	if id == "" {
		return "node"
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func escapeMermaidLabel(label string) string {
	label = strings.ReplaceAll(label, "\n", " ")
	label = strings.ReplaceAll(label, "|", "\\|")
	return label
}
