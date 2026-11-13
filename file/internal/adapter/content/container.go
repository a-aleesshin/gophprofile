package content

import (
	"archive/zip"
	"bytes"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

const (
	mimePDF  = "application/pdf"
	mimeDOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimeZIP  = "application/zip"
)

var (
	pdfHeader  = []byte("%PDF-")
	pdfTrailer = []byte("%%EOF")
)

const pdfTrailerWindow = 2048

func validatePDF(body []byte) domain.RejectReason {
	if !bytes.HasPrefix(body, pdfHeader) {
		return domain.RejectReasonContainerInvalid
	}

	tail := body
	if len(tail) > pdfTrailerWindow {
		tail = tail[len(tail)-pdfTrailerWindow:]
	}

	if !bytes.Contains(tail, pdfTrailer) {
		return domain.RejectReasonContainerInvalid
	}

	return domain.RejectReason{}
}

func validateDOCX(body []byte, policy zipLimits) domain.RejectReason {
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return domain.RejectReasonContainerInvalid
	}

	if policy.MaxEntries > 0 && len(archive.File) > policy.MaxEntries {
		return domain.RejectReasonContainerTooLarge
	}

	var (
		total       uint64
		hasTypes    bool
		hasDocument bool
	)

	for _, entry := range archive.File {
		switch entry.Name {
		case "[Content_Types].xml":
			hasTypes = true
		case "word/document.xml":
			hasDocument = true
		}

		if policy.MaxEntryBytes > 0 && entry.UncompressedSize64 > uint64(policy.MaxEntryBytes) {
			return domain.RejectReasonContainerTooLarge
		}

		total += entry.UncompressedSize64

		if policy.MaxTotalBytes > 0 && total > uint64(policy.MaxTotalBytes) {
			return domain.RejectReasonContainerTooLarge
		}
	}

	if policy.MaxRatio > 0 && len(body) > 0 &&
		total > uint64(len(body))*uint64(policy.MaxRatio) {
		return domain.RejectReasonContainerTooLarge
	}

	if !hasTypes || !hasDocument {
		return domain.RejectReasonContainerInvalid
	}

	return domain.RejectReason{}
}

type zipLimits struct {
	MaxEntries    int
	MaxEntryBytes int64
	MaxTotalBytes int64
	MaxRatio      int
}
