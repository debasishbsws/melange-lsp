package parser

import (
	"gopkg.in/yaml.v3"
)

// MelangeConfig represents a simplified melange configuration.
type MelangeConfig struct {
	Package struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
		Epoch   int    `yaml:"epoch"`
	} `yaml:"package"`
	Environment map[string]interface{} `yaml:"environment"`
	Pipeline    []PipelineStep         `yaml:"pipeline"`
	Subpackages []Subpackage           `yaml:"subpackages"`
	Vars        map[string]string      `yaml:"vars"`
	Data        []RangeData            `yaml:"data"`
}

// PipelineStep represents a step in the pipeline.
type PipelineStep struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	Runs string `yaml:"runs"`
}

// RangeData represents a named data source used with range: directives.
type RangeData struct {
	Name  string            `yaml:"name"`
	Items map[string]string `yaml:"items"`
}

// Subpackage represents a subpackage definition.
type Subpackage struct {
	Name  string `yaml:"name"`
	Range string `yaml:"range"`
}

// Parse parses a melange YAML configuration.
func Parse(content string) (*MelangeConfig, error) {
	var config MelangeConfig
	err := yaml.Unmarshal([]byte(content), &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}
