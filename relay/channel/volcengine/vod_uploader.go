// Package volcengine - VOD upload bridge wrapper.
//
// The real implementation lives in `relay/channel/task/volcvod` (next to
// the SigV4 signer it depends on), so the volcvod task adaptor and this
// (volcengine) adaptor can both call it without a circular package import.
//
// This file re-exports the public types + UploadMediaToVOD as a stable
// import surface for code that lives in the volcengine package
// (asr.go calls UploadMediaToVOD via the asrUploadFn seam).
package volcengine

import (
	"github.com/QuantumNous/new-api/relay/channel/task/volcvod"
)

// VodUploadOpts is re-exported from the volcvod package; see
// `relay/channel/task/volcvod/uploader.go` for full docs.
type VodUploadOpts = volcvod.VodUploadOpts

// VodUploadResult is re-exported from the volcvod package.
type VodUploadResult = volcvod.VodUploadResult

// VodUploadError is re-exported from the volcvod package.
type VodUploadError = volcvod.VodUploadError

// UploadMediaToVOD uploads `body` to Volcengine VOD and returns the resulting
// Vid (+ metadata). See volcvod.UploadMediaToVOD for the full contract.
//
// This thin wrapper preserves the original function path callers may have
// referenced (`volcengine.UploadMediaToVOD`) while keeping the implementation
// in the package that owns the SigV4 signer.
func UploadMediaToVOD(body []byte, opts VodUploadOpts, contentType string) (*VodUploadResult, error) {
	return volcvod.UploadMediaToVOD(body, opts, contentType)
}

// inferContentTypeFromFilename is a re-export with a lower-case shim name so
// ASR (asr.go) and any future internal caller can keep using the unexported
// helper they were written against.
func inferContentTypeFromFilename(filename string) string {
	return volcvod.InferContentTypeFromFilename(filename)
}

func inferExtFromFilename(filename string) string {
	return volcvod.InferExtFromFilename(filename)
}

func inferAudioFormatFromFilename(filename string) string {
	return volcvod.InferAudioFormatFromFilename(filename)
}

// truncate clips `s` to at most `n` runes worth of bytes (byte-wise; callers
// use it for log snippets where rune fidelity doesn't matter).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
