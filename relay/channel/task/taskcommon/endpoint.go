package taskcommon

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// ResolveEndpointURL resolves a validated same-origin path template below the
// configured channel base URL. The only supported variable is task_id.
func ResolveEndpointURL(baseURL, pathTemplate string, variables map[string]string) (string, error) {
	taskID, hasTaskID := variables["task_id"]
	for variable := range variables {
		if variable != "task_id" {
			return "", fmt.Errorf("unsupported endpoint variable: %s", variable)
		}
	}
	if err := dto.ValidateTaskEndpointPath(pathTemplate, hasTaskID); err != nil {
		return "", err
	}
	if strings.TrimSpace(pathTemplate) == "" {
		return "", fmt.Errorf("endpoint path must not be blank")
	}

	resolvedTemplate := pathTemplate
	if hasTaskID {
		pathPart, queryPart, hasQuery := strings.Cut(resolvedTemplate, "?")
		pathPart = strings.Replace(pathPart, dto.TaskIDPlaceholder, url.PathEscape(taskID), 1)
		if hasQuery {
			queryPart = strings.Replace(queryPart, dto.TaskIDPlaceholder, url.QueryEscape(taskID), 1)
			resolvedTemplate = pathPart + "?" + queryPart
		} else {
			resolvedTemplate = pathPart
		}
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.Opaque != "" {
		return "", fmt.Errorf("base URL must be an absolute http or https URL")
	}
	if base.Fragment != "" {
		return "", fmt.Errorf("base URL must not contain a fragment")
	}

	endpoint, err := url.Parse(resolvedTemplate)
	if err != nil {
		return "", fmt.Errorf("parse endpoint path: %w", err)
	}

	joinedEscapedPath := strings.TrimSuffix(base.EscapedPath(), "/") + endpoint.EscapedPath()
	joinedPath, err := url.PathUnescape(joinedEscapedPath)
	if err != nil {
		return "", fmt.Errorf("decode resolved endpoint path: %w", err)
	}

	resolved := *base
	resolved.Path = joinedPath
	resolved.RawPath = joinedEscapedPath
	resolved.RawQuery = endpoint.RawQuery
	resolved.ForceQuery = endpoint.ForceQuery
	resolved.Fragment = ""
	resolved.RawFragment = ""
	if !strings.EqualFold(resolved.Scheme, base.Scheme) || !strings.EqualFold(resolved.Host, base.Host) {
		return "", fmt.Errorf("resolved endpoint must remain on the base URL origin")
	}

	return resolved.String(), nil
}
