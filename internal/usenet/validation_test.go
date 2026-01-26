package usenet

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	metapb "github.com/javi11/altmount/internal/metadata/proto"
	"github.com/javi11/altmount/internal/pool"
	"github.com/javi11/nntppool/v2"
	"github.com/javi11/nntppool/v2/pkg/nntpcli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockPoolManager
type MockPoolManager struct {
	mock.Mock
}

func (m *MockPoolManager) GetPool() (nntppool.UsenetConnectionPool, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(nntppool.UsenetConnectionPool), args.Error(1)
}

func (m *MockPoolManager) SetProviders(providers []nntppool.UsenetProviderConfig) error {
	return nil
}
func (m *MockPoolManager) ClearPool() error {
	return nil
}
func (m *MockPoolManager) HasPool() bool {
	return true
}
func (m *MockPoolManager) GetMetrics() (pool.MetricsSnapshot, error) {
	return pool.MetricsSnapshot{}, nil
}

// MockConnectionPool
type MockConnectionPool struct {
	mock.Mock
}

func (m *MockConnectionPool) Stat(ctx context.Context, id string, groups []string) (int, error) {
	args := m.Called(ctx, id, groups)
	return args.Int(0), args.Error(1)
}

func (m *MockConnectionPool) Body(ctx context.Context, id string, w io.Writer, groups []string) (int64, error) {
	args := m.Called(ctx, id, w, groups)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockConnectionPool) BodyBatch(ctx context.Context, group string, requests []nntppool.BodyBatchRequest) []nntppool.BodyBatchResult {
	args := m.Called(ctx, group, requests)
	return args.Get(0).([]nntppool.BodyBatchResult)
}

func (m *MockConnectionPool) GetConnection(ctx context.Context, skipProviders []string, useBackupProviders bool) (nntppool.PooledConnection, error) {
	args := m.Called(ctx, skipProviders, useBackupProviders)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(nntppool.PooledConnection), args.Error(1)
}

func (m *MockConnectionPool) BodyReader(ctx context.Context, msgID string, nntpGroups []string) (nntpcli.ArticleBodyReader, error) {
	args := m.Called(ctx, msgID, nntpGroups)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(nntpcli.ArticleBodyReader), args.Error(1)
}

func (m *MockConnectionPool) TestProviderPipelineSupport(ctx context.Context, providerHost string, testMsgID string) (bool, int, error) {
	args := m.Called(ctx, providerHost, testMsgID)
	return args.Bool(0), args.Int(1), args.Error(2)
}

func (m *MockConnectionPool) GetProvidersInfo() []nntppool.ProviderInfo {
	args := m.Called()
	return args.Get(0).([]nntppool.ProviderInfo)
}

func (m *MockConnectionPool) GetProviderStatus(providerID string) (*nntppool.ProviderInfo, bool) {
	args := m.Called(providerID)
	if args.Get(0) == nil {
		return nil, args.Bool(1)
	}
	return args.Get(0).(*nntppool.ProviderInfo), args.Bool(1)
}

func (m *MockConnectionPool) GetMetrics() *nntppool.PoolMetrics {
	args := m.Called()
	return args.Get(0).(*nntppool.PoolMetrics)
}

func (m *MockConnectionPool) GetMetricsSnapshot() nntppool.PoolMetricsSnapshot {
	args := m.Called()
	return args.Get(0).(nntppool.PoolMetricsSnapshot)
}

func (m *MockConnectionPool) Post(ctx context.Context, w io.Reader) error {
	return nil
}
func (m *MockConnectionPool) Head(ctx context.Context, id string, groups []string) (string, error) {
	return "", nil
}
func (m *MockConnectionPool) Quit()                     {}
func (m *MockConnectionPool) GetMaxProviders() int      { return 1 }
func (m *MockConnectionPool) GetMinProviders() int      { return 1 }
func (m *MockConnectionPool) GetActiveConnections() int { return 0 }
func (m *MockConnectionPool) GetTotalConnections() int  { return 0 }

func TestValidateSegmentAvailabilityDetailed_Hybrid(t *testing.T) {
	// Setup
	mockPool := new(MockConnectionPool)
	mockManager := new(MockPoolManager)
	mockManager.On("GetPool").Return(mockPool, nil)

	segments := []*metapb.SegmentData{
		{Id: "seg1", SegmentSize: 1000},
	}

	// Test hybrid mode (verifyData = true)
	// Expect Body call
	mockPool.On("Body", mock.Anything, "seg1", mock.Anything, mock.Anything).Return(int64(10), ErrLimitReached).Once()

	result, err := ValidateSegmentAvailabilityDetailed(
		context.Background(),
		segments,
		mockManager,
		1,
		100,
		nil,
		time.Second,
		true, // verifyData
	)

	assert.NoError(t, err)
	assert.Equal(t, 0, result.MissingCount)
	assert.Equal(t, 1, result.TotalChecked)

	mockPool.AssertExpectations(t)
	mockManager.AssertExpectations(t)
}

func TestValidateSegmentAvailabilityDetailed_Hybrid_Failure(t *testing.T) {
	// Setup
	mockPool := new(MockConnectionPool)
	mockManager := new(MockPoolManager)
	mockManager.On("GetPool").Return(mockPool, nil)

	segments := []*metapb.SegmentData{
		{Id: "seg1", SegmentSize: 1000},
	}

	// Test hybrid mode (verifyData = true)
	// Expect Body call returning generic error (e.g. 430 No Such Article)
	mockPool.On("Body", mock.Anything, "seg1", mock.Anything, mock.Anything).Return(int64(0), fmt.Errorf("article not found")).Once()

	result, err := ValidateSegmentAvailabilityDetailed(
		context.Background(),
		segments,
		mockManager,
		1,
		100,
		nil,
		time.Second,
		true, // verifyData
	)

	// Our function accumulates errors internally and returns result
	assert.NoError(t, err)
	assert.Equal(t, 1, result.MissingCount)
	assert.Equal(t, 1, result.TotalChecked)

	mockPool.AssertExpectations(t)
	mockManager.AssertExpectations(t)
}
