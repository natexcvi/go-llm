// Code generated by MockGen. DO NOT EDIT.
// Source: engine.go

// Package mocks is a generated GoMock package.
package mocks

import (
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	engines "github.com/natexcvi/go-llm/engines"
)

// MockLLM is a mock of LLM interface.
type MockLLM struct {
	ctrl     *gomock.Controller
	recorder *MockLLMMockRecorder
}

// MockLLMMockRecorder is the mock recorder for MockLLM.
type MockLLMMockRecorder struct {
	mock *MockLLM
}

// NewMockLLM creates a new mock instance.
func NewMockLLM(ctrl *gomock.Controller) *MockLLM {
	mock := &MockLLM{ctrl: ctrl}
	mock.recorder = &MockLLMMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockLLM) EXPECT() *MockLLMMockRecorder {
	return m.recorder
}

// Predict mocks base method.
func (m *MockLLM) Predict(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Predict", prompt)
	ret0, _ := ret[0].(*engines.ChatMessage)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Predict indicates an expected call of Predict.
func (mr *MockLLMMockRecorder) Predict(prompt interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Predict", reflect.TypeOf((*MockLLM)(nil).Predict), prompt)
}