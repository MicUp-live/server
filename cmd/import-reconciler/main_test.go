package main

import (
	"context"
	"errors"
	"testing"

	"micup.live/server/internal/models"
)

type mockReconcilerService struct {
	reconciledCount int
	reconcileErr    error
	exhaustedJobs   []*models.ImportJob
	exhaustedErr    error
	resetCalledFor  string
	resetErr        error
}

func (m *mockReconcilerService) ReconcileUndispatchedJobs(_ context.Context, _ int) (int, error) {
	if m.reconcileErr != nil {
		return 0, m.reconcileErr
	}
	return m.reconciledCount, nil
}

func (m *mockReconcilerService) ListExhaustedImportJobs(_ context.Context, _ int) ([]*models.ImportJob, error) {
	if m.exhaustedErr != nil {
		return nil, m.exhaustedErr
	}
	return m.exhaustedJobs, nil
}

func (m *mockReconcilerService) ResetImportJobDispatch(_ context.Context, jobID string) error {
	m.resetCalledFor = jobID
	return m.resetErr
}

func TestRunReconciliation_Success(t *testing.T) {
	svc := &mockReconcilerService{
		reconciledCount: 3,
		exhaustedJobs: []*models.ImportJob{
			{ID: "job-ex-1", SongbookID: "sb-1", DispatchAttempts: 5, Error: "sqs unreachable"},
		},
	}

	err := runReconciliation(context.Background(), svc, 5)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestRunReconciliation_ReconcileErrorPropagates(t *testing.T) {
	injectedErr := errors.New("dynamodb outage")
	svc := &mockReconcilerService{
		reconcileErr: injectedErr,
	}

	err := runReconciliation(context.Background(), svc, 5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, injectedErr) {
		t.Fatalf("expected injected error, got: %v", err)
	}
}

func TestRunReconciliation_ExhaustedInspectionErrorPropagates(t *testing.T) {
	injectedErr := errors.New("dynamodb scan error")
	svc := &mockReconcilerService{
		reconciledCount: 2,
		exhaustedErr:    injectedErr,
	}

	err := runReconciliation(context.Background(), svc, 5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, injectedErr) {
		t.Fatalf("expected injected error, got: %v", err)
	}
}

func TestRunReconciliation_AggregatedReconcileErrorPropagates(t *testing.T) {
	err1 := errors.New("dynamodb timeout on job-1")
	err2 := errors.New("dynamodb timeout on job-2")
	injectedErr := errors.Join(err1, err2)
	svc := &mockReconcilerService{
		reconciledCount: 1,
		reconcileErr:    injectedErr,
	}

	err := runReconciliation(context.Background(), svc, 5)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, err1) || !errors.Is(err, err2) {
		t.Fatalf("expected aggregated errors to propagate, got: %v", err)
	}
}
