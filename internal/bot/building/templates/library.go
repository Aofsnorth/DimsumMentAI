package templates

import (
	"encoding/json"
	"fmt"
	"strings"

	"bedrock-ai/internal/bot/building/common"
)

// TemplateLibrary manages loaded templates.
type TemplateLibrary struct {
	templates map[string]*common.Template
}

// NewTemplateLibrary creates a new TemplateLibrary instance.
func NewTemplateLibrary() *TemplateLibrary {
	return &TemplateLibrary{
		templates: make(map[string]*common.Template),
	}
}

func (tl *TemplateLibrary) getKey(structureType, sizeCategory string) string {
	return fmt.Sprintf("%s_%s", strings.ToLower(structureType), strings.ToLower(sizeCategory))
}

// RegisterTemplate registers a template in the library.
func (tl *TemplateLibrary) RegisterTemplate(structureType, sizeCategory string, template *common.Template) {
	key := tl.getKey(structureType, sizeCategory)
	tl.templates[key] = template
}

// HasTemplate checks if a template exists in the library.
func (tl *TemplateLibrary) HasTemplate(structureType, sizeCategory string) bool {
	return tl.templates[tl.getKey(structureType, sizeCategory)] != nil
}

// GetTemplate retrieves a copy of the registered template.
func (tl *TemplateLibrary) GetTemplate(structureType, sizeCategory string) *common.Template {
	tmpl := tl.templates[tl.getKey(structureType, sizeCategory)]
	if tmpl == nil {
		return nil
	}

	data, err := json.Marshal(tmpl)
	if err != nil {
		return nil
	}

	var copyTmpl common.Template
	_ = json.Unmarshal(data, &copyTmpl)
	return &copyTmpl
}

// LoadEmbeddedTemplates loads templates packaged in the Go binary.
func (tl *TemplateLibrary) LoadEmbeddedTemplates() error {
	entries, err := templateFS.ReadDir("house")
	if err != nil {
		return fmt.Errorf("read embedded dir: %w", err)
	}

	loaded := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := "house/" + entry.Name()
		data, err := templateFS.ReadFile(path)
		if err != nil {
			continue
		}

		var tmpl common.Template
		if err := json.Unmarshal(data, &tmpl); err != nil {
			continue
		}

		val := tl.ValidateTemplate(&tmpl)
		if !val.Valid {
			continue
		}

		tl.RegisterTemplate(tmpl.StructureType, tmpl.SizeCategory, &tmpl)
		loaded++
	}
	return nil
}

// GetLibrarySummary returns all templates registered.
func (tl *TemplateLibrary) GetLibrarySummary() string {
	var summary []string
	for key, template := range tl.templates {
		counts := make(map[string]int)
		for _, b := range template.Blocks {
			counts[b.Type]++
		}

		var reqs []string
		for name, count := range counts {
			reqs = append(reqs, fmt.Sprintf("%s:%d", name, count))
		}
		summary = append(summary, fmt.Sprintf("%s: %s", key, strings.Join(reqs, ", ")))
	}
	return strings.Join(summary, " | ")
}
