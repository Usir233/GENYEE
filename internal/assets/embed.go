// Package assets embeds static binary fixtures used for health and model probes.
package assets

import _ "embed"

// ASRTestWAV holds the embedded WAV fixture used when probing ASR model endpoints.
//
//go:embed asr_test.wav
var ASRTestWAV []byte
