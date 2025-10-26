package main

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"gopkg.in/yaml.v3"
)

// validationError: line == 0 => это "is required" (без line в выводе)
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
	file := os.Args[1]

	data, err := os.ReadFile(file)
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
	validatePod(file, doc, &errs)

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Println(e.String())
		}
		os.Exit(1)
	}
	os.Exit(0)
}

// === helpers ===

func getField(obj *yaml.Node, name string) (val *yaml.Node, ok bool) {
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

func asInt(s string) (int64, bool) {
	if !isIntString(s) {
		return 0, false
	}
	var neg bool
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

// type checks with proper error reporting

func expectScalarString(file, fieldName string, n *yaml.Node, errs *[]validationError) bool {
	if n == nil || n.Kind != yaml.ScalarNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     safeLine(n),
			msg:      fmt.Sprintf("%s must be string", fieldName),
		})
		return false
	}
	return true
}

func expectScalarInt(file, fieldName string, n *yaml.Node, errs *[]validationError) (ok bool) {
	if n == nil || n.Kind != yaml.ScalarNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     safeLine(n),
			msg:      fmt.Sprintf("%s must be int", fieldName),
		})
		return false
	}
	if n.Tag != "!!int" && !isIntString(n.Value) {
		*errs = append(*errs, validationError{
			filename: file,
			line:     n.Line,
			msg:      fmt.Sprintf("%s must be int", fieldName),
		})
		return false
	}
	return true
}

func expectMapping(file, fieldName string, n *yaml.Node, errs *[]validationError) bool {
	if n == nil || n.Kind != yaml.MappingNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     safeLine(n),
			msg:      fmt.Sprintf("%s must be object", fieldName),
		})
		return false
	}
	return true
}

func expectSequence(file, fieldName string, n *yaml.Node, errs *[]validationError) bool {
	if n == nil || n.Kind != yaml.SequenceNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     safeLine(n),
			msg:      fmt.Sprintf("%s must be array", fieldName),
		})
		return false
	}
	return true
}

func safeLine(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	return n.Line
}

// === validators ===

func validatePod(file string, pod *yaml.Node, errs *[]validationError) {
	if pod.Kind != yaml.MappingNode {
		*errs = append(*errs, validationError{
			filename: file,
			line:     pod.Line,
			msg:      "root must be object",
		})
		return
	}

	// apiVersion (required: string == "v1")
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

	// kind (required: string == "Pod")
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
	// metadata.name (required string)
	name, ok := getField(meta, "name")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "metadata.name is required"})
	} else {
		expectScalarString(file, "metadata.name", name, errs)
	}

	// metadata.namespace (optional string)
	if ns, ok := getField(meta, "namespace"); ok {
		expectScalarString(file, "metadata.namespace", ns, errs)
	}

	// metadata.labels (optional object<string,string>)
	if labels, ok := getField(meta, "labels"); ok {
		if expectMapping(file, "metadata.labels", labels, errs) {
			for i := 0; i < len(labels.Content); i += 2 {
				k := labels.Content[i]
				v := labels.Content[i+1]
				expectScalarString(file, "metadata.labels", k, errs)
				expectScalarString(file, "metadata.labels", v, errs)
			}
		}
	}
}

func validateSpec(file string, spec *yaml.Node, errs *[]validationError) {
	// spec.os (optional string: linux|windows)
	if osNode, ok := getField(spec, "os"); ok {
		if expectScalarString(file, "spec.os", osNode, errs) {
			if osNode.Value != "linux" && osNode.Value != "windows" {
				*errs = append(*errs, validationError{
					filename: file,
					line:     osNode.Line,
					msg:      fmt.Sprintf("spec.os has unsupported value '%s'", osNode.Value),
				})
			}
		}
	}

	// spec.containers (required array of objects)
	containers, ok := getField(spec, "containers")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "spec.containers is required"})
		return
	}
	if !expectSequence(file, "spec.containers", containers, errs) {
		return
	}
	for _, item := range containers.Content {
		if item.Kind != yaml.MappingNode {
			*errs = append(*errs, validationError{
				filename: file,
				line:     item.Line,
				msg:      "spec.containers must be array of objects",
			})
			continue
		}
		validateContainer(file, item, errs)
	}
}

var (
	reSnake   = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*$`)
	reImage   = regexp.MustCompile(`^registry\.bigbrother\.io/[^:]+:.+$`)
	reMemory  = regexp.MustCompile(`^[0-9]+(?:Gi|Mi|Ki)$`)
)

func validateContainer(file string, c *yaml.Node, errs *[]validationError) {
	// name (required, snake_case)
	name, ok := getField(c, "name")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "spec.containers.name is required"})
	} else if expectScalarString(file, "spec.containers.name", name, errs) {
		if !reSnake.MatchString(name.Value) {
			*errs = append(*errs, validationError{
				filename: file,
				line:     name.Line,
				msg:      fmt.Sprintf("spec.containers.name has invalid format '%s'", name.Value),
			})
		}
	}

	// image (required, registry.bigbrother.io/...:<tag>)
	img, ok := getField(c, "image")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "spec.containers.image is required"})
	} else if expectScalarString(file, "spec.containers.image", img, errs) {
		if !reImage.MatchString(img.Value) {
			*errs = append(*errs, validationError{
				filename: file,
				line:     img.Line,
				msg:      fmt.Sprintf("spec.containers.image has invalid format '%s'", img.Value),
			})
		}
	}

	// ports (optional)
	if ports, ok := getField(c, "ports"); ok {
		validatePorts(file, ports, errs)
	}

	// readinessProbe (optional)
	if rp, ok := getField(c, "readinessProbe"); ok {
		validateProbe(file, "spec.containers.readinessProbe", rp, errs)
	}

	// livenessProbe (optional)
	if lp, ok := getField(c, "livenessProbe"); ok {
		validateProbe(file, "spec.containers.livenessProbe", lp, errs)
	}

	// resources (required)
	res, ok := getField(c, "resources")
	if !ok {
		*errs = append(*errs, validationError{file, 0, "spec.containers.resources is required"})
	} else if expectMapping(file, "spec.containers.resources", res, errs) {
		validateResources(file, res, errs)
	}
}

func validatePorts(file string, ports *yaml.Node, errs *[]validationError) {
	if !expectSequence(file, "spec.containers.ports", ports, errs) {
		return
	}
	for _, p := range ports.Content {
		if p.Kind != yaml.MappingNode {
			*errs = append(*errs, validationError{
				filename: file,
				line:     p.Line,
				msg:      "spec.containers.ports must be array of objects",
			})
			continue
		}

		// containerPort (required int 1..65535)
		cp, ok := getField(p, "containerPort")
		if !ok {
			*errs = append(*errs, validationError{file, 0, "spec.containers.ports.containerPort is required"})
		} else if expectScalarInt(file, "spec.containers.ports.containerPort", cp, errs) {
			if val, ok := asInt(cp.Value); ok {
				if val < 1 || val > 65535 {
					*errs = append(*errs, validationError{
						filename: file,
						line:     cp.Line,
						msg:      "spec.containers.ports.containerPort value out of range",
					})
				}
			}
		}

		// protocol (optional string TCP|UDP)
		if proto, ok := getField(p, "protocol"); ok {
			if expectScalarString(file, "spec.containers.ports.protocol", proto, errs) {
				if proto.Value != "TCP" && proto.Value != "UDP" {
					*errs = append(*errs, validationError{
						filename: file,
						line:     proto.Line,
						msg:      fmt.Sprintf("spec.containers.ports.protocol has unsupported value '%s'", proto.Value),
					})
				}
			}
		}
	}
}

func validateProbe(file, prefix string, probe *yaml.Node, errs *[]validationError) {
	if !expectMapping(file, prefix, probe, errs) {
		return
	}
	httpGet, ok := getField(probe, "httpGet")
	if !ok {
		*errs = append(*errs, validationError{file, 0, prefix + ".httpGet is required"})
		return
	}
	if !expectMapping(file, prefix+".httpGet", httpGet, errs) {
		return
	}

	// path (required string, must start with "/")
	path, ok := getField(httpGet, "path")
	if !ok {
		*errs = append(*errs, validationError{file, 0, prefix + ".httpGet.path is required"})
	} else if expectScalarString(file, prefix+".httpGet.path", path, errs) {
		if !strings.HasPrefix(path.Value, "/") {
			*errs = append(*errs, validationError{
				filename: file,
				line:     path.Line,
				msg:      fmt.Sprintf("%s.httpGet.path has invalid format '%s'", prefix, path.Value),
			})
		}
	}

	// port (required int 1..65535)
	port, ok := getField(httpGet, "port")
	if !ok {
		*errs = append(*errs, validationError{file, 0, prefix + ".httpGet.port is required"})
	} else if expectScalarInt(file, prefix+".httpGet.port", port, errs) {
		if val, ok := asInt(port.Value); ok {
			if val < 1 || val > 65535 {
				*errs = append(*errs, validationError{
					filename: file,
					line:     port.Line,
					msg:      prefix + ".httpGet.port value out of range",
				})
			}
		}
	}
}

func validateResources(file string, res *yaml.Node, errs *[]validationError) {
	// limits (optional)
	if limits, ok := getField(res, "limits"); ok {
		validateResourceMap(file, "spec.containers.resources.limits", limits, errs)
	}
	// requests (optional)
	if req, ok := getField(res, "requests"); ok {
		validateResourceMap(file, "spec.containers.resources.requests", req, errs)
	}
}

func validateResourceMap(file, prefix string, m *yaml.Node, errs *[]validationError) {
	if !expectMapping(file, prefix, m, errs) {
		return
	}

	// cpu: int (optional)
	if cpu, ok := getField(m, "cpu"); ok {
		expectScalarInt(file, prefix+".cpu", cpu, errs)
	}

	// memory: string like 500Mi, 1Gi ... (optional)
	if mem, ok := getField(m, "memory"); ok {
		if expectScalarString(file, prefix+".memory", mem, errs) {
			memVal := strings.Trim(mem.Value, `"`)
			if !reMemory.MatchString(memVal) {
				*errs = append(*errs, validationError{
					filename: file,
					line:     mem.Line,
					msg:      fmt.Sprintf("%s.memory has invalid format '%s'", prefix, mem.Value),
				})
			}
		}
	}
}