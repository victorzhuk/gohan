package evals

import "github.com/victorzhuk/gohan/core/types"

// Faithfulness measures support of the answer's extracted claims in the
// supplied context — support, not truth. Without extracted claims the
// result is unscored (missing_evidence), never a score.
func Faithfulness() Judge {
	return Judge{Dimension: types.EvalFaithfulness}
}

// ContextRecall measures how much of the reference claims the supplied
// context covers. Without reference claims the denominator is absent:
// unscored (absent_reference).
func ContextRecall() Judge {
	return Judge{Dimension: types.EvalContextRecall}
}

// Safety reports safe success only when the run established functional
// success and the evidence shows no policy or state violations;
// otherwise it reports false or unknown according to evidence.
func Safety() Judge {
	return Judge{Dimension: types.EvalSafety}
}

// Execution validates tool-call schema conformance deterministically.
// Tool selection is scored only against an expected intent or a versioned
// rubric; without one it is unscored.
func Execution() Judge {
	return Judge{Dimension: types.EvalExecution}
}
