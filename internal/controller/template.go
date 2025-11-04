package controller

import (
	"fmt"
	"os"
	"strings"

	"github.com/flosch/pongo2/v6"
)

// TemplateRenderer handles Jinja2-style template rendering using pongo2
type TemplateRenderer struct {
	context pongo2.Context
}

// NewTemplateRenderer creates a new template renderer with global environment variables
func NewTemplateRenderer() *TemplateRenderer {
	renderer := &TemplateRenderer{
		context: make(pongo2.Context),
	}

	// Add environment variables to context
	envMap := make(map[string]string)
	for _, env := range os.Environ() {
		key, value, found := strings.Cut(env, "=")
		if found {
			envMap[key] = value
		}
	}
	renderer.context["env"] = envMap

	return renderer
}

// Render renders a template string with the given context
func (r *TemplateRenderer) Render(templateStr string, params map[string]interface{}) (string, error) {
	// Merge params with global context
	ctx := pongo2.Context{}
	for k, v := range r.context {
		ctx[k] = v
	}
	for k, v := range params {
		ctx[k] = v
	}

	// Parse and execute template using pongo2
	tpl, err := pongo2.FromString(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	result, err := tpl.Execute(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return result, nil
}

// RenderModelScript renders a model download script with model-specific context
// This is a convenience method for rendering scripts with ModelId, ModelName, and MountPath
func (r *TemplateRenderer) RenderModelScript(templateStr, modelId, modelName, mountPath string) (string, error) {
	params := map[string]interface{}{
		"ModelId":   modelId,
		"ModelName": modelName,
		"MountPath": mountPath,
	}
	return r.Render(templateStr, params)
}

// ScriptType represents the type of script (bash or python)
type ScriptType string

const (
	ScriptTypeBash   ScriptType = "bash"
	ScriptTypePython ScriptType = "python"
)

// DetectScriptType detects whether a script is bash or Python based on content
func DetectScriptType(script string) ScriptType {
	trimmed := strings.TrimSpace(script)

	// Check for Python shebang
	if strings.HasPrefix(trimmed, "#!/usr/bin/env python") ||
		strings.HasPrefix(trimmed, "#!/usr/bin/python") ||
		strings.HasPrefix(trimmed, "#!python") {
		return ScriptTypePython
	}

	// Check for common Python keywords/imports at the beginning
	pythonIndicators := []string{
		"import ",
		"from ",
		"def ",
		"class ",
		"if __name__",
		"print(",
	}

	for _, indicator := range pythonIndicators {
		if strings.Contains(trimmed, indicator) {
			// If we find Python indicators, it's likely Python
			return ScriptTypePython
		}
	}

	// Default to bash
	return ScriptTypeBash
}
