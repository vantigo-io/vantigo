package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

// fakeConnector is a minimal driver.Connector whose Connect can be told to
// fail, and that counts how many times the inner connector was actually
// asked to dial.
type fakeConnector struct {
	connects int
	fail     bool
}

func (f *fakeConnector) Connect(context.Context) (driver.Conn, error) {
	f.connects++
	if f.fail {
		return nil, errors.New("boom")
	}
	return fakeConn{}, nil
}

func (f *fakeConnector) Driver() driver.Driver { return fakeDriver{} }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not implemented") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not implemented") }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("not implemented") }

func TestSingleSessionConnector_AFailedFirstConnectDoesNotConsumeTheSession(t *testing.T) {
	inner := &fakeConnector{fail: true}
	c := newSingleSessionConnector(inner)

	if _, err := c.Connect(context.Background()); err == nil {
		t.Fatal("expected the inner connector's error")
	}

	inner.fail = false
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatalf("retry after a failed connect: %v", err)
	}
	if inner.connects != 2 {
		t.Fatalf("inner connects = %d, want 2", inner.connects)
	}
}

func TestSingleSessionConnector_ASecondConnectAfterSuccessIsRefusedWithoutDialing(t *testing.T) {
	inner := &fakeConnector{}
	c := newSingleSessionConnector(inner)

	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatalf("first connect: %v", err)
	}

	_, err := c.Connect(context.Background())
	if !errors.Is(err, errSessionLost) {
		t.Fatalf("err = %v, want errSessionLost", err)
	}
	if inner.connects != 1 {
		t.Fatalf("inner connects = %d, want 1 (the inner connector must not be dialed again)", inner.connects)
	}
}
