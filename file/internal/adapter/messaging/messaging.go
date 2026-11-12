package messaging

import (
	"context"

	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
)

const (
	DestinationProcessFile   = "file.process"
	DestinationDeleteObjects = "objects.delete"
	DestinationFileEvents    = "gophprofile.file.v1"
)

const (
	TypeProcessFile   = "ProcessFile"
	TypeDeleteObjects = "DeleteObjects"
	TypeFileProcessed = "FileProcessed"
	TypeFileRejected  = "FileRejected"
	TypeFileDeleted   = "FileDeleted"
)

type processFileData struct {
	FileID     string   `json:"file_id"`
	StagingKey string   `json:"staging_key"`
	Variants   []string `json:"variants"`
}

type deleteObjectsData struct {
	FileID      string   `json:"file_id"`
	StorageKeys []string `json:"storage_keys"`
}

type variantData struct {
	Name      string `json:"name"`
	Ext       string `json:"ext"`
	MimeType  string `json:"mime_type"`
	Width     int32  `json:"width"`
	Height    int32  `json:"height"`
	SizeBytes int64  `json:"size_bytes"`
	ETag      string `json:"etag"`
}

type fileProcessedData struct {
	FileID       string        `json:"file_id"`
	OwnerRef     string        `json:"owner_ref"`
	OriginalName string        `json:"original_name"`
	MimeType     string        `json:"mime_type"`
	SizeBytes    int64         `json:"size_bytes"`
	Width        int32         `json:"width"`
	Height       int32         `json:"height"`
	Variants     []variantData `json:"variants"`
}

type fileRejectedData struct {
	FileID   string `json:"file_id"`
	OwnerRef string `json:"owner_ref"`
	Reason   string `json:"reason"`
}

type fileDeletedData struct {
	FileID   string `json:"file_id"`
	OwnerRef string `json:"owner_ref"`
}

type Publisher struct {
	writer *outbox.Writer
}

func NewPublisher(writer *outbox.Writer) *Publisher {
	return &Publisher{writer: writer}
}

func (p *Publisher) EnqueueProcessFile(
	ctx context.Context,
	cmd usecase.ProcessFileCommand,
) error {
	variants := make([]string, 0, len(cmd.Variants))
	for _, variant := range cmd.Variants {
		variants = append(variants, variant.String())
	}

	return p.writer.Write(ctx, outbox.Message{
		Transport:    outbox.TransportRabbit,
		Destination:  DestinationProcessFile,
		PartitionKey: cmd.FileID.String(),
		Type:         TypeProcessFile,
		Data: processFileData{
			FileID:     cmd.FileID.String(),
			StagingKey: cmd.StagingKey.String(),
			Variants:   variants,
		},
	})
}

func (p *Publisher) EnqueueDeleteObjects(
	ctx context.Context,
	cmd usecase.DeleteObjectsCommand,
) error {
	keys := make([]string, 0, len(cmd.Locations))
	for _, location := range cmd.Locations {
		keys = append(keys, location.String())
	}

	return p.writer.Write(ctx, outbox.Message{
		Transport:    outbox.TransportRabbit,
		Destination:  DestinationDeleteObjects,
		PartitionKey: cmd.FileID.String(),
		Type:         TypeDeleteObjects,
		Data:         deleteObjectsData{FileID: cmd.FileID.String(), StorageKeys: keys},
	})
}

func (p *Publisher) PublishFileProcessed(
	ctx context.Context,
	fact usecase.FileProcessedFact,
) error {
	variants := make([]variantData, 0, len(fact.Variants))

	for _, variant := range fact.Variants {
		variants = append(variants, variantData{
			Name:      variant.Name().String(),
			Ext:       variant.Ext().String(),
			MimeType:  variant.MimeType().String(),
			Width:     variant.Dimensions().Width(),
			Height:    variant.Dimensions().Height(),
			SizeBytes: variant.Size().Bytes(),
			ETag:      variant.Checksum().String(),
		})
	}

	return p.writer.Write(ctx, outbox.Message{
		Transport:    outbox.TransportKafka,
		Destination:  DestinationFileEvents,
		PartitionKey: fact.FileID.String(),
		Type:         TypeFileProcessed,
		Data: fileProcessedData{
			FileID:       fact.FileID.String(),
			OwnerRef:     fact.OwnerRef.String(),
			OriginalName: fact.OriginalName.String(),
			MimeType:     fact.MimeType.String(),
			SizeBytes:    fact.Size.Bytes(),
			Width:        fact.Dimensions.Width(),
			Height:       fact.Dimensions.Height(),
			Variants:     variants,
		},
	})
}

func (p *Publisher) PublishFileRejected(
	ctx context.Context,
	fact usecase.FileRejectedFact,
) error {
	return p.writer.Write(ctx, outbox.Message{
		Transport:    outbox.TransportKafka,
		Destination:  DestinationFileEvents,
		PartitionKey: fact.FileID.String(),
		Type:         TypeFileRejected,
		Data: fileRejectedData{
			FileID:   fact.FileID.String(),
			OwnerRef: fact.OwnerRef.String(),
			Reason:   fact.Reason.String(),
		},
	})
}

func (p *Publisher) PublishFileDeleted(
	ctx context.Context,
	fact usecase.FileDeletedFact,
) error {
	return p.writer.Write(ctx, outbox.Message{
		Transport:    outbox.TransportKafka,
		Destination:  DestinationFileEvents,
		PartitionKey: fact.FileID.String(),
		Type:         TypeFileDeleted,
		Data:         fileDeletedData{FileID: fact.FileID.String(), OwnerRef: fact.OwnerRef.String()},
	})
}
