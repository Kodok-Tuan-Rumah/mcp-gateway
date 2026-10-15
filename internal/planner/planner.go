package planner

type Step struct {
	ToolName string
	Params   map[string]interface{}
}

type Plan struct {
	Query   string
	TraceID string
	Steps   []Step
}
