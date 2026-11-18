package usecase

import (
	"context"
	"fmt"
)

type StorageUsage struct {
	Kind    string
	Bytes   int64
	Objects int64
}

type StatusCount struct {
	Status string
	Count  int64
}

type storageReader interface {
	StorageUsage(ctx context.Context) ([]StorageUsage, error)
	CountByStatus(ctx context.Context) ([]StatusCount, error)
}

type ObserveStorageResult struct {
	Usage  []StorageUsage
	Counts []StatusCount
}

type ObserveStorageHandler struct {
	files storageReader
}

func NewObserveStorageHandler(files storageReader) (*ObserveStorageHandler, error) {
	if files == nil {
		return nil, fmt.Errorf("%w: a storage reader is required", ErrInvalidCommand)
	}

	return &ObserveStorageHandler{files: files}, nil
}

func (h *ObserveStorageHandler) Handle(ctx context.Context) (ObserveStorageResult, error) {
	usage, err := h.files.StorageUsage(ctx)
	if err != nil {
		return ObserveStorageResult{}, err
	}

	counts, err := h.files.CountByStatus(ctx)
	if err != nil {
		return ObserveStorageResult{}, err
	}

	return ObserveStorageResult{Usage: usage, Counts: counts}, nil
}
