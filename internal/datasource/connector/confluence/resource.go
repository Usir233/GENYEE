package confluence

import (
	"fmt"
	"strings"
)

type resourceKind uint8

const (
	resourceSpace resourceKind = iota
	resourcePage
)

// resourceRef is the canonical, persisted representation of a selection.
// Space IDs deliberately remain unprefixed for backwards compatibility.
type resourceRef struct {
	Kind    resourceKind
	SpaceID string
	PageID  string
}

func makePageResourceID(spaceID, pageID string) string {
	return "page:" + spaceID + ":" + pageID
}

func validateConfluenceSegment(kind, value string) error {
	if value == "" || value != strings.TrimSpace(value) || value == "." || value == ".." {
		return fmt.Errorf("invalid Confluence %s ID", kind)
	}
	if strings.ContainsAny(value, "/\\?#") {
		return fmt.Errorf("invalid Confluence %s ID", kind)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid Confluence %s ID", kind)
		}
	}
	return nil
}

func parseResourceID(id string) (resourceRef, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return resourceRef{}, fmt.Errorf("invalid empty Confluence resource ID")
	}
	if !strings.HasPrefix(id, "page:") {
		if strings.Contains(id, ":") {
			return resourceRef{}, fmt.Errorf("invalid Confluence space resource ID %q", id)
		}
		if err := validateConfluenceSegment("space", id); err != nil {
			return resourceRef{}, err
		}
		return resourceRef{Kind: resourceSpace, SpaceID: id}, nil
	}
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] != "page" {
		return resourceRef{}, fmt.Errorf("invalid Confluence page resource ID %q", id)
	}
	if err := validateConfluenceSegment("space", parts[1]); err != nil {
		return resourceRef{}, err
	}
	if err := validateConfluenceSegment("page", parts[2]); err != nil {
		return resourceRef{}, err
	}
	return resourceRef{Kind: resourcePage, SpaceID: parts[1], PageID: parts[2]}, nil
}
