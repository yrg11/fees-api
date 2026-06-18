package fees

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

type WorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *WorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *WorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestWorkflowSuite(t *testing.T) {
	suite.Run(t, new(WorkflowTestSuite))
}

func (s *WorkflowTestSuite) Test_NewBill_AutoClosesAtPeriodEnd() {
	now := time.Now().UTC()
	input := FeePeriodWorkflowInput{
		BillID:      1,
		CustomerID:  "cust_1",
		Currency:    CurrencyUSD,
		PeriodStart: now,
		PeriodEnd:   now.Add(30 * 24 * time.Hour),
	}

	// Mock the CloseBillActivity — expect it to be called with reason "period_ended".
	s.env.OnActivity(CloseBillActivity, mock.Anything, CloseBillActivityInput{
		BillID: 1,
		Reason: "period_ended",
	}).Return(CloseBillActivityOutput{
		Bill: Bill{
			ID:       1,
			Status:   BillStatusClosed,
			Currency: CurrencyUSD,
		},
		LineItems: nil,
	}, nil)

	s.env.ExecuteWorkflow(FeePeriodWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result FeePeriodWorkflowResult
	s.NoError(s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), int64(1), result.BillID)
	assert.True(s.T(), result.Closed)
	assert.Equal(s.T(), "period_ended", result.CloseReason)
}

func (s *WorkflowTestSuite) Test_AddLineItemSignal_PersistsItem() {
	now := time.Now().UTC()
	input := FeePeriodWorkflowInput{
		BillID:      1,
		CustomerID:  "cust_1",
		Currency:    CurrencyUSD,
		PeriodStart: now,
		PeriodEnd:   now.Add(30 * 24 * time.Hour),
	}

	// Mock AddLineItemActivity.
	s.env.OnActivity(AddLineItemActivity, mock.Anything, AddLineItemActivityInput{
		BillID: 1,
		Input: AddLineItemRequest{
			Description: "API usage",
			AmountMinor: 4999,
			Currency:    CurrencyUSD,
		},
	}).Return(AddLineItemActivityOutput{
		LineItem: LineItem{
			ID:          1,
			BillID:      1,
			Description: "API usage",
			AmountMinor: 4999,
			Currency:    CurrencyUSD,
			CreatedAt:   now,
		},
	}, nil)

	// Mock CloseBillActivity for period end.
	s.env.OnActivity(CloseBillActivity, mock.Anything, mock.Anything).Return(CloseBillActivityOutput{
		Bill:      Bill{ID: 1, Status: BillStatusClosed},
		LineItems: nil,
	}, nil)

	// Send add line item signal after a short delay.
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalAddLineItem, AddLineItemSignal{
			Description: "API usage",
			AmountMinor: 4999,
			Currency:    CurrencyUSD,
		})
	}, 1*time.Second)

	s.env.ExecuteWorkflow(FeePeriodWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result FeePeriodWorkflowResult
	s.NoError(s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), int64(4999), result.TotalAmountMinor)
	assert.Equal(s.T(), 1, result.ItemCount)
}

func (s *WorkflowTestSuite) Test_CloseBillSignal_ClosesBeforePeriodEnd() {
	now := time.Now().UTC()
	input := FeePeriodWorkflowInput{
		BillID:      1,
		CustomerID:  "cust_1",
		Currency:    CurrencyUSD,
		PeriodStart: now,
		PeriodEnd:   now.Add(30 * 24 * time.Hour),
	}

	// Mock CloseBillActivity.
	s.env.OnActivity(CloseBillActivity, mock.Anything, CloseBillActivityInput{
		BillID: 1,
		Reason: "manual_close",
	}).Return(CloseBillActivityOutput{
		Bill:      Bill{ID: 1, Status: BillStatusClosed},
		LineItems: nil,
	}, nil)

	// Send close signal after 1 second (well before period end).
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalCloseBill, CloseBillSignal{
			Reason: "manual_close",
		})
	}, 1*time.Second)

	s.env.ExecuteWorkflow(FeePeriodWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result FeePeriodWorkflowResult
	s.NoError(s.env.GetWorkflowResult(&result))
	assert.True(s.T(), result.Closed)
	assert.Equal(s.T(), "manual_close", result.CloseReason)
}

func (s *WorkflowTestSuite) Test_MultipleLineItems_AccumulatesTotal() {
	now := time.Now().UTC()
	input := FeePeriodWorkflowInput{
		BillID:      1,
		CustomerID:  "cust_1",
		Currency:    CurrencyUSD,
		PeriodStart: now,
		PeriodEnd:   now.Add(30 * 24 * time.Hour),
	}

	// Mock activity for first line item.
	s.env.OnActivity(AddLineItemActivity, mock.Anything, AddLineItemActivityInput{
		BillID: 1,
		Input: AddLineItemRequest{
			Description: "Item 1",
			AmountMinor: 1000,
			Currency:    CurrencyUSD,
		},
	}).Return(AddLineItemActivityOutput{
		LineItem: LineItem{ID: 1, BillID: 1, Description: "Item 1", AmountMinor: 1000, Currency: CurrencyUSD, CreatedAt: now},
	}, nil)

	// Mock activity for second line item.
	s.env.OnActivity(AddLineItemActivity, mock.Anything, AddLineItemActivityInput{
		BillID: 1,
		Input: AddLineItemRequest{
			Description: "Item 2",
			AmountMinor: 2500,
			Currency:    CurrencyUSD,
		},
	}).Return(AddLineItemActivityOutput{
		LineItem: LineItem{ID: 2, BillID: 1, Description: "Item 2", AmountMinor: 2500, Currency: CurrencyUSD, CreatedAt: now},
	}, nil)

	// Mock close activity.
	s.env.OnActivity(CloseBillActivity, mock.Anything, mock.Anything).Return(CloseBillActivityOutput{
		Bill:      Bill{ID: 1, Status: BillStatusClosed},
		LineItems: nil,
	}, nil)

	// Send two line items then close.
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalAddLineItem, AddLineItemSignal{
			Description: "Item 1",
			AmountMinor: 1000,
			Currency:    CurrencyUSD,
		})
	}, 1*time.Second)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalAddLineItem, AddLineItemSignal{
			Description: "Item 2",
			AmountMinor: 2500,
			Currency:    CurrencyUSD,
		})
	}, 2*time.Second)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalCloseBill, CloseBillSignal{Reason: "manual_close"})
	}, 3*time.Second)

	s.env.ExecuteWorkflow(FeePeriodWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result FeePeriodWorkflowResult
	s.NoError(s.env.GetWorkflowResult(&result))
	assert.Equal(s.T(), int64(3500), result.TotalAmountMinor)
	assert.Equal(s.T(), 2, result.ItemCount)
}

func (s *WorkflowTestSuite) Test_QueryBillState_ReturnsCurrentState() {
	now := time.Now().UTC()
	input := FeePeriodWorkflowInput{
		BillID:      1,
		CustomerID:  "cust_1",
		Currency:    CurrencyUSD,
		PeriodStart: now,
		PeriodEnd:   now.Add(30 * 24 * time.Hour),
	}

	// Mock add line item activity.
	s.env.OnActivity(AddLineItemActivity, mock.Anything, mock.Anything).Return(AddLineItemActivityOutput{
		LineItem: LineItem{ID: 1, BillID: 1, Description: "Test", AmountMinor: 500, Currency: CurrencyUSD, CreatedAt: now},
	}, nil)

	// Mock close activity.
	s.env.OnActivity(CloseBillActivity, mock.Anything, mock.Anything).Return(CloseBillActivityOutput{
		Bill:      Bill{ID: 1, Status: BillStatusClosed},
		LineItems: nil,
	}, nil)

	// Add a line item, then query state, then close.
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalAddLineItem, AddLineItemSignal{
			Description: "Test",
			AmountMinor: 500,
			Currency:    CurrencyUSD,
		})
	}, 1*time.Second)

	// Query after the line item is processed.
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryBillState)
		s.NoError(err)

		var state BillWorkflowState
		s.NoError(result.Get(&state))
		assert.Equal(s.T(), BillStatusOpen, state.Status)
		assert.Equal(s.T(), int64(500), state.TotalAmountMinor)
		assert.Len(s.T(), state.LineItems, 1)

		// Now close.
		s.env.SignalWorkflow(SignalCloseBill, CloseBillSignal{Reason: "manual_close"})
	}, 2*time.Second)

	s.env.ExecuteWorkflow(FeePeriodWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_PeriodAlreadyEnded_ClosesImmediately() {
	// If period_end is in the past, workflow should close immediately.
	past := time.Now().UTC().Add(-1 * time.Hour)
	input := FeePeriodWorkflowInput{
		BillID:      1,
		CustomerID:  "cust_1",
		Currency:    CurrencyUSD,
		PeriodStart: past.Add(-24 * time.Hour),
		PeriodEnd:   past,
	}

	s.env.OnActivity(CloseBillActivity, mock.Anything, CloseBillActivityInput{
		BillID: 1,
		Reason: "period_ended",
	}).Return(CloseBillActivityOutput{
		Bill:      Bill{ID: 1, Status: BillStatusClosed},
		LineItems: nil,
	}, nil)

	s.env.ExecuteWorkflow(FeePeriodWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result FeePeriodWorkflowResult
	s.NoError(s.env.GetWorkflowResult(&result))
	assert.True(s.T(), result.Closed)
	assert.Equal(s.T(), "period_ended", result.CloseReason)
}
