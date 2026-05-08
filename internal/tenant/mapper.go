package tenant

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// RoomMappingType represents the type of room mapping
type RoomMappingType int

const (
	MappingTypeIndividual RoomMappingType = iota
	MappingTypeRange
	MappingTypePattern
)

// RoomMappingEntry represents a single mapping entry with its type
type RoomMappingEntry struct {
	Type         RoomMappingType
	RoomNumber   string
	RoomEnd      string
	Extension    string
	ExtensionEnd string
	MatchPattern *regexp.Regexp
}

// RoomMapper handles room-to-extension mapping
type RoomMapper struct {
	prefix   string
	mappings []RoomMappingEntry // ordered list for priority (individual > range > pattern)
	byRoom   map[string]string  // cache for fast individual lookups
	byExt    map[string]string  // reverse cache
}

// NewRoomMapper creates a new room mapper with optional prefix
func NewRoomMapper(prefix string) *RoomMapper {
	return &RoomMapper{
		prefix:   prefix,
		mappings: make([]RoomMappingEntry, 0),
		byRoom:   make(map[string]string),
		byExt:    make(map[string]string),
	}
}

// parseRange converts a room number string to integer for range comparison
func parseRoomNumber(s string) (int, bool) {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// GetExtension returns the extension for a room number
func (m *RoomMapper) GetExtension(room string) (string, error) {
	room = strings.TrimSpace(room)

	// Check individual exact matches first
	if ext, ok := m.byRoom[room]; ok {
		return ext, nil
	}

	// Check ranges
	for _, entry := range m.mappings {
		if entry.Type != MappingTypeRange {
			continue
		}
		start, startOK := parseRoomNumber(entry.RoomNumber)
		end, endOK := parseRoomNumber(entry.RoomEnd)
		roomNum, roomOK := parseRoomNumber(room)
		if !startOK || !endOK || !roomOK {
			continue
		}
		if roomNum >= start && roomNum <= end {
			extStart, _ := strconv.Atoi(entry.Extension)
			offset := roomNum - start
			return strconv.Itoa(extStart + offset), nil
		}
	}

	// Check pattern matches
	for _, entry := range m.mappings {
		if entry.Type != MappingTypePattern || entry.MatchPattern == nil {
			continue
		}
		if entry.MatchPattern.MatchString(room) {
			return entry.Extension, nil
		}
	}

	// Apply prefix as fallback
	if m.prefix != "" {
		return m.prefix + room, nil
	}

	return room, nil
}

// GetRoom returns the room number for an extension
func (m *RoomMapper) GetRoom(extension string) (string, error) {
	// Check reverse cache first
	if room, ok := m.byExt[extension]; ok {
		return room, nil
	}

	// Strip prefix if configured
	if m.prefix != "" && strings.HasPrefix(extension, m.prefix) {
		return strings.TrimPrefix(extension, m.prefix), nil
	}

	return extension, nil
}

// SetMapping adds a custom individual room-to-extension mapping
func (m *RoomMapper) SetMapping(room, extension string) error {
	if room == "" || extension == "" {
		return fmt.Errorf("room and extension cannot be empty")
	}
	m.byRoom[room] = extension
	m.byExt[extension] = room
	return nil
}

// SetRange adds a range mapping (e.g., rooms 101-105 → extensions 201-205)
func (m *RoomMapper) SetRange(roomStart, roomEnd, extStart, extEnd string) error {
	if roomStart == "" || roomEnd == "" || extStart == "" || extEnd == "" {
		return fmt.Errorf("range boundaries cannot be empty")
	}
	rs, rsOK := parseRoomNumber(roomStart)
	re, reOK := parseRoomNumber(roomEnd)
	es, _ := strconv.Atoi(extStart)
	_ = es // stored in Extension for offset calculation
	_, _ = parseRoomNumber(extEnd)
	if !rsOK || !reOK {
		return fmt.Errorf("range values must be numeric")
	}
	if re < rs {
		return fmt.Errorf("room end must be >= room start")
	}
	m.mappings = append(m.mappings, RoomMappingEntry{
		Type:         MappingTypeRange,
		RoomNumber:   roomStart,
		RoomEnd:      roomEnd,
		Extension:    extStart,
		ExtensionEnd: extEnd,
	})
	return nil
}

// SetPattern adds a regex-based mapping
func (m *RoomMapper) SetPattern(pattern, extension string) error {
	if pattern == "" || extension == "" {
		return fmt.Errorf("pattern and extension cannot be empty")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid regex pattern: %w", err)
	}
	m.mappings = append(m.mappings, RoomMappingEntry{
		Type:         MappingTypePattern,
		MatchPattern: re,
		Extension:    extension,
	})
	return nil
}

// RemoveMapping removes a custom mapping
func (m *RoomMapper) RemoveMapping(room string) {
	if ext, ok := m.byRoom[room]; ok {
		delete(m.byExt, ext)
	}
	delete(m.byRoom, room)
}

// ListMappings returns all individual mappings
func (m *RoomMapper) ListMappings() map[string]string {
	result := make(map[string]string, len(m.byRoom))
	for k, v := range m.byRoom {
		result[k] = v
	}
	return result
}
