package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// validationError: line == 0 => вывод без ":<line>"
type validationError struct {
	filename string
	line     int
	msg      string
}

func (e validationError) String() string {
	if e.line == 0 {
		return e.msg
	}
	return fmt.Sprintf("%s:%d %s", e.filename, e.line, e.msg)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: yamlvalidator <file>")
		os.Exit(1)
	}
	fullPath := os.Args[1]
	displayName := filepath.Base(fullPath)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		log.Printf("cannot read file content: %v", err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		log.Printf("cannot unmarshal file content: %v", err)
		os.Exit(1)
	}

	if len(root.Content) == 0 {
		log.Printf("empty YAML")
		os.Exit(1)
	}
	doc := root.Content[0]

	var errs []validationError
	validatePod(displayName, doc, &errs)

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Println(e.String())
		}
		os.Exit(1)
	}

	os.Exit(0)
}

// ---------------- helpers ----------------

func getField(obj *yaml.Node, name string) (*yaml.Node, bool) {
	if obj == nil || obj.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i < len(obj.Content); i += 2 {
		k := obj.Content[i]
		v := obj.Content[i+1]
		if k.Value == name {
			return v, true
		}
	}
	return nil, false
}

// integer utils
func isIntString(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
		if s == "" {
			return false
		}
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func parseInt(s string) (int64, bool) {
	if !isIntString(s) {
		return 0, false
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	var n int64
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

func safeLine(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	return n.Line
}

// typed expectations with canonical field names

func expectScalarString(file, field string, n *yaml.Node, errs *[]validationError) bool {
	if n == nil || n.Kind != yaml.ScalarNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     safeLine(n),
			msg:      fmt.Sprintf("%s must be string", field),
		})
		return false
	}
	return true
}

func expectScalarInt(file, field string, n *yaml.Node, errs *[]validationError) bool {
	if n == nil || n.Kind != yaml.ScalarNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     safeLine(n),
			msg:      fmt.Sprintf("%s must be int", field),
		})
		return false
	}
	if n.Tag != "!!int" && !isIntString(n.Value) {
		*errs = append(*errs, validationError{
			filename: file,
			line:     n.Line,
			msg:      fmt.Sprintf("%s must be int", field),
		})
		return false
	}
	return true
}

func expectMapping(file, field string, n *yaml.Node, errs *[]validationError) bool {
	if n == nil || n.Kind != yaml.MappingNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     safeLine(n),
			msg:      fmt.Sprintf("%s must be object", field),
		})
		return false
	}
	return true
}

func expectSequence(file, field string, n *yaml.Node, errs *[]validationError) bool {
	if n == nil || n.Kind != yaml.SequenceNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     safeLine(n),
			msg:      fmt.Sprintf("%s must be array", field),
		})
		return false
	}
	return true
}

// ---------------- validators ----------------

func validatePod(file string, pod *yaml.Node, errs *[]validationError) {
	if pod.Kind != yaml.MappingNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     pod.Line,
			msg:      "root must be object",
		})
		return
	}

	// apiVersion (required string == "v1")
	apiVersion, ok := getField(pod, "apiVersion")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "apiVersion is required"})
	} else if expectScalarString(file, "apiVersion", apiVersion, errs) {
		if apiVersion.Value != "v1" {
			*errs = append(*errs, validationError{
				filename: file,
				line:     apiVersion.Line,
				msg:      fmt.Sprintf("apiVersion has unsupported value '%s'", apiVersion.Value),
			})
		}
	}

	// kind (required string == "Pod")
	kind, ok := getField(pod, "kind")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "kind is required"})
	} else if expectScalarString(file, "kind", kind, errs) {
		if kind.Value != "Pod" {
			*errs = append(*errs, validationError{
				filename: file,
				line:     kind.Line,
				msg:      fmt.Sprintf("kind has unsupported value '%s'", kind.Value),
			})
		}
	}

	// metadata (required object)
	meta, ok := getField(pod, "metadata")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "metadata is required"})
	} else if expectMapping(file, "metadata", meta, errs) {
		validateMetadata(file, meta, errs)
	}

	// spec (required object)
	spec, ok := getField(pod, "spec")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "spec is required"})
	} else if expectMapping(file, "spec", spec, errs) {
		validateSpec(file, spec, errs)
	}
}

func validateMetadata(file string, meta *yaml.Node, errs *[]validationError) {
	// name (required non-empty string)
	name, ok := getField(meta, "name")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "name is required"})
	} else if expectScalarString(file, "name", name, errs) {
		if strings.TrimSpace(name.Value) == "" {
			// пустая строка => тоже "name is required", но уже с линией
			*errs = append(*errs, validationError{
				filename: file,
				line:     name.Line,
				msg:      "name is required",
			})
		}
	}

	// namespace (optional string)
	if ns, ok := getField(meta, "namespace"); ok {
		expectScalarString(file, "namespace", ns, errs)
	}

	// labels (optional: object<string,string>)
	if labels, ok := getField(meta, "labels"); ok {
		if expectMapping(file, "labels", labels, errs) {
			for i := 0; i < len(labels.Content); i += 2 {
				k := labels.Content[i]
				v := labels.Content[i+1]
				expectScalarString(file, "labels", k, errs)
				expectScalarString(file, "labels", v, errs)
			}
		}
	}
}

func validateSpec(file string, spec *yaml.Node, errs *[]validationError) {
	// os (optional string in {linux,windows})
	if osNode, ok := getField(spec, "os"); ok {
		if expectScalarString(file, "os", osNode, errs) {
			if osNode.Value != "linux" && osNode.Value != "windows" {
				*errs = append(*errs, validationError{
					filename: file,
					line:     osNode.Line,
					msg:      fmt.Sprintf("os has unsupported value '%s'", osNode.Value),
				})
			}
		}
	}

	// containers (required array of objects)
	containers, ok := getField(spec, "containers")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "containers is required"})
		return
	}
	if !expectSequence(file, "containers", containers, errs) {
		return
	}

	for _, c := range containers.Content {
		if c.Kind != yaml.MappingNode {
			*errs = append(*errs, validationError{
				filename: file,
				line:     c.Line,
				msg:      "containers must be array of objects",
			})
			continue
		}
		validateContainer(file, c, errs)
	}
}

var (
	reSnake  = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*$`)
	reImage  = regexp.MustCompile(`^registry\.bigbrother\.io/[^:]+:.+$`)
	reMemory = regexp.MustCompile(`^[0-9]+(?:Gi|Mi|Ki)$`)
)

func validateContainer(file string, c *yaml.Node, errs *[]validationError) {
	// name (required snake_case)
	nameNode, ok := getField(c, "name")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "name is required"})
	} else if expectScalarString(file, "name", nameNode, errs) {
		if strings.TrimSpace(nameNode.Value) == "" {
			*errs = append(*errs, validationError{
				filename: file,
				line:     nameNode.Line,
				msg:      "name is required",
			})
		} else if !reSnake.MatchString(nameNode.Value) {
			*errs = append(*errs, validationError{
				filename: file,
				line:     nameNode.Line,
				msg:      fmt.Sprintf("name has invalid format '%s'", nameNode.Value),
			})
		}
	}

	// image (required registry.bigbrother.io/...:<tag>)
	imageNode, ok := getField(c, "image")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "image is required"})
	} else if expectScalarString(file, "image", imageNode, errs) {
		if !reImage.MatchString(imageNode.Value) {
			*errs = append(*errs, validationError{
				filename: file,
				line:     imageNode.Line,
				msg:      fmt.Sprintf("image has invalid format '%s'", imageNode.Value),
			})
		}
	}

	// ports (optional)
	if portsNode, ok := getField(c, "ports"); ok {
		validatePorts(file, portsNode, errs)
	}

	// readinessProbe (optional)
	if rpNode, ok := getField(c, "readinessProbe"); ok {
		validateProbe(file, rpNode, errs)
	}

	// livenessProbe (optional)
	if lpNode, ok := getField(c, "livenessProbe"); ok {
		validateProbe(file, lpNode, errs)
	}

	// resources (required)
	resNode, ok := getField(c, "resources")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "resources is required"})
	} else if expectMapping(file, "resources", resNode, errs) {
		validateResources(file, resNode, errs)
	}
}

func validatePorts(file string, ports *yaml.Node, errs *[]validationError) {
	if !expectSequence(file, "ports", ports, errs) {
		return
	}
	for _, p := range ports.Content {
		if p.Kind != yaml.MappingNode {
			*errs = append(*errs, validationError{
				filename: file,
				line:     p.Line,
				msg:      "ports must be array of objects",
			})
			continue
		}

		// containerPort (required int 1..65535)
		cpNode, ok := getField(p, "containerPort")
		if !ok {
			*errs = append(*errs, validationError{file, 0, "containerPort is required"})
		} else if expectScalarInt(file, "containerPort", cpNode, errs) {
			if val, ok := parseInt(cpNode.Value); ok {
				if val < 1 || val > 65535 {
					*errs = append(*errs, validationError{
						filename: file,
						line:     cpNode.Line,
						msg:      "containerPort value out of range",
					})
				}
			}
		}

		// protocol (optional string TCP|UDP)
		if protoNode, ok := getField(p, "protocol"); ok {
			if expectScalarString(file, "protocol", protoNode, errs) {
				if protoNode.Value != "TCP" && protoNode.Value != "UDP" {
					*errs = append(*errs, validationError{
						filename: file,
						line:     protoNode.Line,
						msg:      fmt.Sprintf("protocol has unsupported value '%s'", protoNode.Value),
					})
				}
			}
		}
	}
}

// probe: must have httpGet{path,port}
func validateProbe(file string, probe *yaml.Node, errs *[]validationError) {
	if !expectMapping(file, "probe", probe, errs) {
		return
	}
	httpGet, ok := getField(probe, "httpGet")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "httpGet is required"})
		return
	}
	if !expectMapping(file, "httpGet", httpGet, errs) {
		return
	}

	// path (required string, must start with "/")
	pathNode, ok := getField(httpGet, "path")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "path is required"})
	} else if expectScalarString(file, "path", pathNode, errs) {
		if !strings.HasPrefix(pathNode.Value, "/") {
			*errs = append(*errs, validationError{
				filename: file,
				line:     pathNode.Line,
				msg:      fmt.Sprintf("path has invalid format '%s'", pathNode.Value),
			})
		}
	}

	// port (required int 1..65535)
	portNode, ok := getField(httpGet, "port")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "port is required"})
	} else if expectScalarInt(file, "port", portNode, errs) {
		if val, ok := parseInt(portNode.Value); ok {
			if val < 1 || val > 65535 {
				*errs = append(*errs, validationError{
					filename: file,
					line:     portNode.Line,
					msg:      "port value out of range",
				})
			}
		}
	}
}

func validateResources(file string, res *yaml.Node, errs *[]validationError) {
	// limits (optional)
	if limits, ok := getField(res, "limits"); ok {
		validateResourceMap(file, limits, errs)
	}
	// requests (optional)
	if req, ok := getField(res, "requests"); ok {
		validateResourceMap(file, req, errs)
	}
}

// limits/requests: cpu(int), memory("123Mi" / "1Gi" / ...)
func validateResourceMap(file string, m *yaml.Node, errs *[]validationError) {
	if !expectMapping(file, "resources", m, errs) {
		return
	}

	// cpu must be int
	if cpuNode, ok := getField(m, "cpu"); ok {
		if !expectScalarInt(file, "cpu", cpuNode, errs) {
			// ошибка уже добавлена
		}
	}

	// memory must match ^[0-9]+(Gi|Mi|Ki)$
	if memNode, ok := getField(m, "memory"); ok {
		if expectScalarString(file, "memory", memNode, errs) {
			memVal := strings.Trim(memNode.Value, `"`)
			if !reMemory.MatchString(memVal) {
				*errs = append(*errs, validationError{
					filename: file,
					line:     memNode.Line,
					msg:      fmt.Sprintf("memory has invalid format '%s'", memNode.Value),
				})
			}
		}
	}
}
