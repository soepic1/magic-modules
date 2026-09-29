/*
* Copyright 2026 Google LLC. All Rights Reserved.
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
 */
package cmd

import (
	"fmt"
	"magician/provider"
	utils "magician/utility"
	"os"
	"path/filepath"
	"strings"
)

// Classification of a PR test failure against the nightly test history.
const (
	NightlyStatusFailing  = "Failing"
	NightlyStatusFlaky    = "Flaky"
	NightlyStatusPassing  = "Passing"
	NightlyStatusNotFound = "Not found"
)

// Minimum nightly failures within the history window for a test to be considered
// consistently failing in nightly.
const nightlyFailingThreshold = 3

// loadNightlyTestHistory downloads the rolling nightly test history for pVersion.
// It returns nil (without error) when gcs is nil so callers can treat the history as optional.
func loadNightlyTestHistory(pVersion provider.Version, gcs CloudstorageClient) (map[string]*NightlyTestHistory, error) {
	if gcs == nil {
		return nil, nil
	}
	localPath := filepath.Join(os.TempDir(), fmt.Sprintf("nightly-test-history-%s.json", pVersion.String()))
	defer os.Remove(localPath)

	if err := gcs.DownloadFile(nightlyDataBucket, nightlyTestHistoryObjectName(pVersion), localPath); err != nil {
		return nil, fmt.Errorf("failed to download nightly test history: %w", err)
	}
	var report NightlyTestHistoryReport
	if err := utils.ReadFromJson(&report, localPath); err != nil {
		return nil, fmt.Errorf("failed to read nightly test history: %w", err)
	}
	return report.Tests, nil
}

// nightlyTestHistoryUrl links to the rolling history file backing the nightly column.
func nightlyTestHistoryUrl(pVersion provider.Version) string {
	return fmt.Sprintf("https://storage.cloud.google.com/%s/%s", nightlyDataBucket, nightlyTestHistoryObjectName(pVersion))
}

// lookupNightlyHistory finds the history entry for a test name. The name may be a VCR
// subtest name (Parent__sub); the parent test is used as a fallback.
func lookupNightlyHistory(testName string, history map[string]*NightlyTestHistory) *NightlyTestHistory {
	if h, ok := history[strings.ReplaceAll(testName, "__", "/")]; ok {
		return h
	}
	return history[compoundTest(testName)]
}

// classifyNightlyStatus returns how the given test behaves in recent nightly runs.
func classifyNightlyStatus(testName string, history map[string]*NightlyTestHistory) string {
	h := lookupNightlyHistory(testName, history)
	if h == nil {
		return NightlyStatusNotFound
	}
	if h.Failures > 0 && h.LastStatus == "FAILURE" && (h.Failures >= nightlyFailingThreshold || h.Passes == 0) {
		return NightlyStatusFailing
	}
	if h.Failures > 0 && h.Passes > 0 {
		return NightlyStatusFlaky
	}
	if h.Passes > 0 {
		return NightlyStatusPassing
	}
	return NightlyStatusNotFound
}

// nightlyEvidence summarizes the nightly runs behind a status and links each recent failure to its
// debug log, e.g. "2/30 failed: 2026-09-27, 2026-09-28". The dates let a reviewer open the exact
// nightly runs the status was derived from.
func nightlyEvidence(testName string, history map[string]*NightlyTestHistory) string {
	h := lookupNightlyHistory(testName, history)
	if h == nil {
		return ""
	}
	runs := h.Passes + h.Failures + h.Skips
	if runs == 0 {
		return ""
	}
	evidence := fmt.Sprintf("%d/%d failed", h.Failures, runs)
	if len(h.FailureRuns) == 0 {
		return evidence
	}

	dates := make([]string, 0, len(h.FailureRuns))
	for _, run := range h.FailureRuns {
		if run.LogLink == "" {
			dates = append(dates, run.Date)
			continue
		}
		dates = append(dates, fmt.Sprintf("[%s](%s)", run.Date, run.LogLink))
	}
	// The history keeps only the most recent failures, so say so rather than implying it is complete.
	if h.Failures > len(h.FailureRuns) {
		return fmt.Sprintf("%s, latest %d: %s", evidence, len(dates), strings.Join(dates, ", "))
	}
	return fmt.Sprintf("%s: %s", evidence, strings.Join(dates, ", "))
}

// nightlySymbol renders a nightly status label for the PR comment table.
func nightlySymbol(status string) string {
	switch status {
	case NightlyStatusFailing:
		return "🔴 Failing"
	case NightlyStatusFlaky:
		return "🟡 Flaky"
	case NightlyStatusPassing:
		return "🟢 Passing"
	case NightlyStatusNotFound:
		return "Not run"
	default:
		return "-"
	}
}

// nightlyCell renders the nightly column: a status label followed by the runs backing it, each
// failure linked to its nightly debug log so the reviewer can verify the status.
func nightlyCell(row VCRTestTableRow) string {
	if row.NightlyStatus == "" {
		return ""
	}
	label := nightlySymbol(row.NightlyStatus)
	if row.NightlyEvidence == "" {
		return label
	}
	return fmt.Sprintf("%s<br>%s", label, row.NightlyEvidence)
}
