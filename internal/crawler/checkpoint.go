package crawler

import (
	"encoding/json"
	"os"
)

// checkpointItem is the on-disk form of a pending frontier entry.
type checkpointItem struct {
	URL      string `json:"url"`
	Depth    int    `json:"depth"`
	Attempt  int    `json:"attempt"`
	Priority int    `json:"priority"`
}

// saveCheckpoint writes the pending frontier to path atomically (write-temp +
// rename) so a crash mid-write cannot corrupt an existing checkpoint.
func saveCheckpoint(path string, items []frontierItem) error {
	recs := make([]checkpointItem, len(items))
	for i, it := range items {
		recs[i] = checkpointItem{URL: it.url, Depth: it.depth, Attempt: it.attempt, Priority: it.priority}
	}
	data, err := json.Marshal(recs)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadCheckpoint reads a frontier snapshot written by saveCheckpoint.
func loadCheckpoint(path string) ([]frontierItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var recs []checkpointItem
	if err := json.Unmarshal(data, &recs); err != nil {
		return nil, err
	}
	items := make([]frontierItem, len(recs))
	for i, r := range recs {
		items[i] = frontierItem{url: r.URL, depth: r.Depth, attempt: r.Attempt, priority: r.Priority}
	}
	return items, nil
}
