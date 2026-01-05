package tenant

import (
	"fmt"
	"strings"
)

// RoomMapper handles room-to-extension mapping
type RoomMapper struct {
	prefix   string
	mappings map[string]string // room -> extension
	reverse  map[string]string // extension -> room
}

// NewRoomMapper creates a new room mapper with optional prefix
func NewRoomMapper(prefix string) *RoomMapper {
	return &RoomMapper{
		prefix:   prefix,
		mappings: make(map[string]string),
		reverse:  make(map[string]string),
	}
}

// GetExtension returns the extension for a room number
func (m *RoomMapper) GetExtension(room string) (string, error) {
	// Check custom mapping first
	if ext, ok := m.mappings[room]; ok {
		return ext, nil
	}

	// Apply prefix if configured
	room = strings.TrimSpace(room)
	if m.prefix != "" {
		return m.prefix + room, nil
	}

	return room, nil
}

// GetRoom returns the room number for an extension
func (m *RoomMapper) GetRoom(extension string) (string, error) {
	// Check reverse mapping first
	if room, ok := m.reverse[extension]; ok {
		return room, nil
	}

	// Strip prefix if configured
	if m.prefix != "" && strings.HasPrefix(extension, m.prefix) {
		return strings.TrimPrefix(extension, m.prefix), nil
	}

	return extension, nil
}

// SetMapping adds a custom room-to-extension mapping
func (m *RoomMapper) SetMapping(room, extension string) error {
	if room == "" || extension == "" {
		return fmt.Errorf("room and extension cannot be empty")
	}

	m.mappings[room] = extension
	m.reverse[extension] = room
	return nil
}

// RemoveMapping removes a custom mapping
func (m *RoomMapper) RemoveMapping(room string) {
	if ext, ok := m.mappings[room]; ok {
		delete(m.reverse, ext)
	}
	delete(m.mappings, room)
}

// ListMappings returns all custom mappings
func (m *RoomMapper) ListMappings() map[string]string {
	result := make(map[string]string, len(m.mappings))
	for k, v := range m.mappings {
		result[k] = v
	}
	return result
}
