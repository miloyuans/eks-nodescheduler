// agent/reporter/reporter.go
package reporter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"agent/model"
)

func Report(endpoint string, report model.ReportRequest) error {
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report failed: %w", err)
	}

	resp, err := http.Post(endpoint, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("post report failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("report rejected with status %d", resp.StatusCode)
	}

	log.Printf("[INFO] Report accepted by central server")
	return nil
}