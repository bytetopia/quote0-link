package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/bytetopia/quote0-link/app/internal/device"
	"github.com/bytetopia/quote0-link/app/internal/plugin"
)

// frameCache remembers the last frame the panel confirmed, keyed by the
// device's USB serial (or port name). Every send path (manual, hooks, scheduled tasks)
// goes through it, so identical content never triggers an e-ink refresh.
// The panel keeps its image without power and the firmware does not clear
// it on boot, so the cache is persisted and survives app restarts.
type frameCache struct {
	path   string
	Device string `json:"device"`
	Sum    string `json:"sha256"`
}

func loadFrameCache(path string) *frameCache {
	cache := &frameCache{}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, cache)
		}
	}
	cache.path = path
	return cache
}

func frameSum(frame *device.Frame) string {
	sum := sha256.Sum256(frame[:])
	return hex.EncodeToString(sum[:])
}

func (cache *frameCache) same(deviceID string, frame *device.Frame) bool {
	return cache.Sum != "" && cache.Device == deviceID && cache.Sum == frameSum(frame)
}

func (cache *frameCache) remember(deviceID string, frame *device.Frame) {
	cache.Device, cache.Sum = deviceID, frameSum(frame)
	cache.save()
}

// reset forgets the panel content, e.g. after a failed send or maintenance.
func (cache *frameCache) reset() {
	if cache.Sum == "" {
		return
	}
	cache.Device, cache.Sum = "", ""
	cache.save()
}

func (cache *frameCache) save() {
	if cache.path == "" {
		return
	}
	if cache.Sum == "" {
		_ = os.Remove(cache.path)
		return
	}
	if data, err := json.Marshal(cache); err == nil {
		_ = plugin.AtomicWrite(cache.path, data)
	}
}
