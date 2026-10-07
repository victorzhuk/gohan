package std

import (
	"encoding/json"
	"net/http"
	"strconv"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

// ExplainHandler serves the resolved configuration of one native flow as
// JSON. It answers from Stack.Explain alone: no provider call runs and no
// run budget is spent.
func ExplainHandler(stack *gohan.Stack) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeProblem(w, http.StatusMethodNotAllowed, "method "+r.Method+" is not allowed")
			return
		}
		flow := r.URL.Query().Get("flow")
		if flow == "" {
			writeProblem(w, types.ProblemOf(types.ErrInputInvalid).Status, "the flow query parameter is required")
			return
		}
		ex := stack.Explain(flow)
		if ex.Flow == "" {
			writeProblem(w, types.ProblemOf(types.ErrInputInvalid).Status, "no registered flow named "+strconv.Quote(flow))
			return
		}
		sample, err := json.Marshal(ex.Sample) //nolint:staticcheck // MarshalJSONTo strips Verify, which has no wire form
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, err.Error())
			return
		}
		body, err := json.Marshal(struct {
			Flow        string
			Profile     string
			Steps       []chains.StepInfo
			ToolSteps   []chains.StepInfo
			Tools       []chains.ToolInfo
			Strategies  chains.StrategyInfo
			Fallback    string
			Limits      types.RunLimits
			Granularity string
			Sample      json.RawMessage
			Prompts     map[string]string
			Skills      map[string]string
			Release     string
			Warnings    []string
		}{
			Flow:        ex.Flow,
			Profile:     ex.Profile,
			Steps:       ex.Steps,
			ToolSteps:   ex.ToolSteps,
			Tools:       ex.Tools,
			Strategies:  ex.Strategies,
			Fallback:    ex.Fallback,
			Limits:      ex.Limits,
			Granularity: ex.Granularity,
			Sample:      sample,
			Prompts:     ex.Prompts,
			Skills:      ex.Skills,
			Release:     ex.Release,
			Warnings:    ex.Warnings,
		})
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	p := types.ProblemOf(types.ErrInputInvalid)
	p.Status = status
	body, err := json.Marshal(struct {
		Type   string `json:"type"`
		Code   string `json:"code"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}{
		Type:   "https://gohan.dev/problems/" + string(p.Code),
		Code:   string(p.Code),
		Title:  p.Title,
		Status: status,
		Detail: detail,
	})
	if err != nil {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
