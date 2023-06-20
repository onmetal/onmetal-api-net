package provider

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/types"
)

const (
	apiNetPrefix = "onmetal-api-net://"
)

// ParseNetworkInterfaceID parses network interface provider IDs into the name and UID.
// The format of a network interface provider id is as follows:
// onmetal-api-net://<name>#<uid>
func ParseNetworkInterfaceID(id string) (name string, uid types.UID, err error) {
	parts := strings.SplitN(strings.TrimPrefix(id, apiNetPrefix), "#", 3)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid provider id %q", id)
	}

	name = parts[0]
	uid = types.UID(parts[1])
	if allErrs := validation.NameIsDNSLabel(name, false); len(allErrs) != 0 {
		return "", "", fmt.Errorf("name is not a dns label: %v", allErrs)
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return "", "", fmt.Errorf("uid is not a v4 UUID: %w", err)
	}
	return name, uid, nil
}

func GetNetworkInterfaceID(name string, uid types.UID) string {
	return apiNetPrefix + name + "#" + string(uid)
}

// ParseNetworkID parses network provider IDs into the apinet network name.
// The format of a network provider ID is as follows:
// onmetal-api-net://<name>
func ParseNetworkID(id string) (name string, err error) {
	name = strings.TrimPrefix(id, apiNetPrefix)

	if allErrs := validation.NameIsDNSLabel(name, false); len(allErrs) != 0 {
		return "", fmt.Errorf("name is not a dns label: %v", allErrs)
	}
	return name, nil
}

func GetNetworkID(name string) string {
	return apiNetPrefix + name
}
