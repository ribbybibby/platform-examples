package helm

import (
	"fmt"

	"github.com/chainguard-dev/customer-success/scripts/image-mapper/internal/mapper"
	"github.com/google/go-containerregistry/pkg/name"
	"gopkg.in/yaml.v3"
)

// MapValues maps image references in a values file from upstream images to
// Chainguard. It writes the subset of modified values to the output.
func MapValues(m mapper.Mapper, input []byte) ([]byte, error) {
	inputValues := map[string]interface{}{}
	if err := yaml.Unmarshal(input, &inputValues); err != nil {
		return nil, fmt.Errorf("unmarshalling yaml: %w", err)
	}

	outputValues := map[string]interface{}{}
	if err := walkValues([]string{}, inputValues, mapFn(m, outputValues)); err != nil {
		return nil, fmt.Errorf("walking values: %w", err)
	}

	output, err := yaml.Marshal(outputValues)
	if err != nil {
		return nil, fmt.Errorf("marshalling yaml: %w", err)
	}

	return output, nil
}

// walkFn is a function called by walkValues
type walkFn func(path []string, key string, value interface{}) error

// walkValues walks through the values file, calling fn on each key
func walkValues(path []string, values map[string]interface{}, fn walkFn) error {
	for k, v := range values {
		if err := fn(path, k, v); err != nil {
			return err
		}

		if next, ok := v.(map[string]interface{}); ok {
			if err := walkValues(append(path, k), next, fn); err != nil {
				return err
			}
		}
	}

	return nil
}

// mapFn returns a function that maps image references, writing the mapped
// values to outputValues
func mapFn(m mapper.Mapper, outputValues map[string]interface{}) walkFn {
	return func(path []string, key string, value interface{}) error {
		if key != "image" {
			return nil
		}

		switch img := value.(type) {

		// Handle values like:
		//
		//  image: ghcr.io/foo/bar
		case string:
			mapping, err := mapImage(m, img)
			if err == nil {
				setValue(outputValues, append(path, key), mapping.Context().String())
			}

		// Handle values like:
		//
		//  image:
		//    repository: ghcr.io/foo/bar
		//
		//  OR
		//
		//  image:
		//    registry: ghcr.io
		//    repository: foo/bar
		case map[string]interface{}:
			var repo string
			if v, ok := img["repository"]; ok {
				repo, ok = v.(string)
			}
			if repo != "" {
				registry, hasRegistry := img["registry"]
				if v, ok := registry.(string); ok && hasRegistry && registry != "" {
					repo = fmt.Sprintf("%s/%s", v, repo)
				}

				mapping, err := mapImage(m, repo)
				if err == nil {
					if hasRegistry {
						setValue(outputValues, append(path, key, "registry"), mapping.Context().RegistryStr())
						setValue(outputValues, append(path, key, "repository"), mapping.Context().RepositoryStr())
					} else {
						setValue(outputValues, append(path, key, "repository"), mapping.Context().String())
					}

				}
			}
		}

		return nil
	}
}

// mapImage maps the provided image to its Chainguard equivalent
func mapImage(m mapper.Mapper, img string) (name.Reference, error) {
	mapping, err := m.Map(img)
	if err != nil {
		return nil, fmt.Errorf("mapping image: %s: %w", img, err)
	}
	if len(mapping.Results) == 0 {
		return nil, fmt.Errorf("no results found")
	}
	ref, err := name.ParseReference(mapping.Results[0])
	if err != nil {
		return nil, fmt.Errorf("parsing reference: %w", err)
	}

	return ref, nil
}

// setValue sets the value defined by path
func setValue(outputValues map[string]interface{}, path []string, value interface{}) {
	current := outputValues
	for i, k := range path {
		if i == len(path)-1 {
			current[k] = value
			continue
		}

		if _, ok := current[k]; !ok {
			current[k] = map[string]interface{}{}
		}
		current = current[k].(map[string]interface{})
	}
}
