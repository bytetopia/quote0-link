package plugin

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FieldType selects the control the management UI renders for a Field. The UI
// is generated entirely from these declarations, so an integration describes
// its settings form here and needs no front-end code.
type FieldType string

const (
	Text     FieldType = "text"     // single-line input (the default)
	Textarea FieldType = "textarea" // multi-line input
	Number   FieldType = "number"   // integer, bounded by Min/Max when set
	Select   FieldType = "select"   // one of Options
	Toggle   FieldType = "toggle"   // checkbox stored as "true" / "false"
	Secret   FieldType = "secret"   // masked input for API keys and tokens
)

const maxValueLength = 500

// Option is one choice of a Select field.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field declares one setting. Values are always stored as strings; the core
// applies Default, checks Required, Type, Options and Min/Max, and only then
// hands the result to the integration's own Normalize.
type Field struct {
	Key         string    `json:"key"`
	Label       string    `json:"label"`
	Type        FieldType `json:"type,omitempty"`
	Placeholder string    `json:"placeholder,omitempty"`
	Help        string    `json:"help,omitempty"`
	Required    bool      `json:"required,omitempty"`
	Default     string    `json:"default,omitempty"`
	Options     []Option  `json:"options,omitempty"`
	Min         *int      `json:"min,omitempty"`
	Max         *int      `json:"max,omitempty"`
}

// Int is a helper for Field.Min / Field.Max literals.
func Int(value int) *int { return &value }

// Clean validates raw values against fields. Unknown keys are dropped, empty
// values fall back to Default, and toggles are always present.
func Clean(fields []Field, raw map[string]string) (map[string]string, error) {
	settings := map[string]string{}
	for _, field := range fields {
		value := strings.TrimSpace(raw[field.Key])
		if value == "" {
			value = field.Default
		}
		if utf8.RuneCountInString(value) > maxValueLength {
			return nil, fmt.Errorf("%s 过长", field.Label)
		}
		switch field.Type {
		case Toggle:
			on, err := strconv.ParseBool(value)
			if value != "" && err != nil {
				return nil, fmt.Errorf("%s 应为开或关", field.Label)
			}
			value = strconv.FormatBool(on)
		case Number:
			if value == "" {
				break
			}
			number, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("%s 应为整数", field.Label)
			}
			if field.Min != nil && number < *field.Min || field.Max != nil && number > *field.Max {
				return nil, fmt.Errorf("%s 超出范围", field.Label)
			}
			value = strconv.Itoa(number)
		case Select:
			if value != "" && !slices.ContainsFunc(field.Options, func(option Option) bool { return option.Value == value }) {
				return nil, fmt.Errorf("%s 的选项无效", field.Label)
			}
		}
		if value == "" {
			if field.Required {
				return nil, fmt.Errorf("请填写%s", field.Label)
			}
			continue
		}
		settings[field.Key] = value
	}
	return settings, nil
}
