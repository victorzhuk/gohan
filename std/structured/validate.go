package structured

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

// ValidateRepair is the structured-output strategy: validate the model's
// output against the declared type and its bounds, and allow at most Max
// repair turns before ErrStructuredOutput is returned.
type ValidateRepair struct {
	Max int
}

// NewValidateRepair freezes the strategy value the caller passes into Build.
func NewValidateRepair(max int) ValidateRepair {
	return ValidateRepair{Max: max}
}

// Validate checks the bounds declared with the `bounds` struct tag
// ("min=1,max=10" on numeric fields) after decoding, because a constrained
// provider's own schema enforcement cannot be trusted to have applied them.
// A value outside bounds fails with ErrStructuredOutput and never reaches
// business code.
func Validate[Out any](out Out) error {
	v := reflect.ValueOf(out)
	if err := validateBounds(value(v), ""); err != nil {
		return fmt.Errorf("validate structured output: %w: %s", types.ErrStructuredOutput, err)
	}
	return nil
}

// refusalPhrases is the frozen refusal-as-JSON heuristic: a schema-matching
// string value containing one of these phrases classifies the call
// ClassContentPolicy.
var refusalPhrases = []string{
	"cannot assist with that",
	"can't assist with that",
	"cannot help with that",
	"can't help with that",
}

// Classify reports whether out carries a refusal phrased as schema-valid
// JSON, and the class it is classified under. The metric
// gohan.model.refusal_as_json is recorded by the caller; this package
// exposes no metric API.
func Classify(out any) (types.ErrorClass, bool) {
	var phrase string
	if findRefusal(reflect.ValueOf(out), &phrase) {
		return types.ClassContentPolicy, true
	}
	return 0, false
}

func findRefusal(v reflect.Value, phrase *string) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return false
		}
		return findRefusal(v.Elem(), phrase)
	case reflect.String:
		s := strings.ToLower(v.String())
		for _, p := range refusalPhrases {
			if strings.Contains(s, p) {
				*phrase = p
				return true
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if findRefusal(v.Field(i), phrase) {
				return true
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if findRefusal(v.MapIndex(k), phrase) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if findRefusal(v.Index(i), phrase) {
				return true
			}
		}
	}
	return false
}

func value(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return v
		}
		v = v.Elem()
	}
	return v
}

func validateBounds(v reflect.Value, path string) error {
	v = value(v)
	switch v.Kind() {
	case reflect.Invalid:
		return nil
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			f := t.Field(i)
			fp := f.Name
			if path != "" {
				fp = path + "." + f.Name
			}
			if err := checkFieldBounds(v.Field(i), f, fp); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := validateBounds(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if err := validateBounds(v.MapIndex(k), path+"."+fmt.Sprint(k.Interface())); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkFieldBounds(v reflect.Value, f reflect.StructField, path string) error {
	tag, ok := f.Tag.Lookup("bounds")
	if ok && tag == "-" {
		return nil
	}
	if !ok {
		return validateBounds(v, path)
	}
	v = value(v)
	if !isNumber(v.Kind()) {
		return fmt.Errorf("field %s: bounds tag on non-numeric field", path)
	}
	var n float64
	switch kind := v.Kind(); {
	case kind >= reflect.Int && kind <= reflect.Int64:
		n = float64(v.Int())
	case kind >= reflect.Uint && kind <= reflect.Uint64:
		n = float64(v.Uint())
	default:
		n = v.Float()
	}
	for _, part := range strings.Split(tag, ",") {
		op, bound, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return fmt.Errorf("field %s: malformed bounds tag %q", path, tag)
		}
		b, err := strconv.ParseFloat(bound, 64)
		if err != nil {
			return fmt.Errorf("field %s: malformed bounds tag %q", path, tag)
		}
		switch op {
		case "min":
			if n < b {
				return fmt.Errorf("field %s: value %v below minimum %v", path, v.Interface(), b)
			}
		case "max":
			if n > b {
				return fmt.Errorf("field %s: value %v above maximum %v", path, v.Interface(), b)
			}
		default:
			return fmt.Errorf("field %s: malformed bounds tag %q", path, tag)
		}
	}
	return nil
}

func isNumber(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Uint64 || k >= reflect.Float32 && k <= reflect.Float64
}
