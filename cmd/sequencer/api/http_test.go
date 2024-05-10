package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/agiledragon/gomonkey/v2"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/machinefi/ioconnect-go/cmd/srv-did-vc/apis"
	"github.com/machinefi/ioconnect-go/pkg/ioconnect"
	"github.com/machinefi/sprout/apitypes"
	"github.com/machinefi/sprout/auth/didvc"
	"github.com/machinefi/sprout/clients"
	"github.com/machinefi/sprout/cmd/sequencer/persistence"
)

func TestNewHttpServer(t *testing.T) {
	r := require.New(t)

	s := NewHttpServer(nil, uint(1), "", "", nil, nil)
	r.Equal(uint(1), s.aggregationAmount)
}

func TestHttpServer_Run(t *testing.T) {
	r := require.New(t)

	s := &httpServer{
		engine: gin.Default(),
	}

	t.Run("FailedToRun", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		p.ApplyMethodReturn(&gin.Engine{}, "Run", errors.New(t.Name()))

		err := s.Run("")
		r.ErrorContains(err, t.Name())
	})

	t.Run("Success", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		p.ApplyMethodReturn(&gin.Engine{}, "Run", nil)

		err := s.Run("")
		r.NoError(err)
	})
}

func TestHttpServer_verifyToken(t *testing.T) {
	r := require.New(t)

	s := &httpServer{}
	t.Run("FailedToAuthorized", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("authorization", "Bearer valid_token")

		p.ApplyFuncReturn(didvc.VerifyJWTCredential, "", errors.New(t.Name()))
		s.verifyToken(c)
		r.Equal(http.StatusUnauthorized, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("Success", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("authorization", "Bearer valid_token")

		p.ApplyFuncReturn(didvc.VerifyJWTCredential, "clientID", nil)

		s.verifyToken(c)

		clientID, ok := didvc.ClientIDFrom(c.Request.Context())
		r.Equal(true, ok)
		r.Equal("clientID", clientID)
	})
}

func TestHttpServer_handleMessage(t *testing.T) {
	r := require.New(t)

	s := &httpServer{}

	t.Run("FailedToReadBody", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest(http.MethodPost, "/", nil)

		p.ApplyFuncReturn(io.ReadAll, nil, errors.New(t.Name()))
		s.handleMessage(c)
		r.Equal(http.StatusInternalServerError, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("FailedToDecrypt", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{"projectID": 123, "projectVersion": "v1", "data": "some data"}`)))

		p.ApplyFuncReturn(io.ReadAll, []byte("body"), nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "", true)
		p.ApplyMethodReturn(&ioconnect.JWK{}, "DecryptBySenderDID", nil, errors.New(t.Name()))
		s.handleMessage(c)
		r.Equal(http.StatusBadRequest, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("FailedToBindBody", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{"projectID": 123, "projectVersion": "v1", "data": "some data"}`)))

		p.ApplyFuncReturn(io.ReadAll, []byte("body"), nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "", true)
		p.ApplyMethodReturn(&ioconnect.JWK{}, "DecryptBySenderDID", nil, nil)
		//p.ApplyFuncReturn(binding.JSON.BindBody, errors.New(t.Name()))
		s.handleMessage(c)
		r.Equal(http.StatusBadRequest, w.Code)
	})

	t.Run("FailedToVerify", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{"projectID": 123, "projectVersion": "v1", "data": "some data"}`)))

		p.ApplyFuncReturn(io.ReadAll, []byte("body"), nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "clientID", true)
		p.ApplyMethodReturn(&ioconnect.JWK{}, "DecryptBySenderDID", []byte(`{"projectID": 123, "projectVersion": "v1", "data": "some data"}`), nil)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, errors.New(t.Name()))
		s.handleMessage(c)
		r.Equal(http.StatusUnauthorized, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("FailedToSave", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{"projectID": 123, "projectVersion": "v1", "data": "some data"}`)))

		p.ApplyFuncReturn(io.ReadAll, []byte("body"), nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "clientID", true)
		p.ApplyMethodReturn(&ioconnect.JWK{}, "DecryptBySenderDID", []byte(`{"projectID": 123, "projectVersion": "v1", "data": "some data"}`), nil)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, nil)
		p.ApplyMethodReturn(&persistence.Persistence{}, "Save", errors.New(t.Name()))
		s.handleMessage(c)
		r.Equal(http.StatusInternalServerError, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("Success", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{"projectID": 123, "projectVersion": "v1", "data": "some data"}`)))

		p.ApplyFuncReturn(io.ReadAll, []byte("body"), nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "clientID", true)
		p.ApplyMethodReturn(&ioconnect.JWK{}, "DecryptBySenderDID", []byte(`{"projectID": 123, "projectVersion": "v1", "data": "some data"}`), nil)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, nil)
		p.ApplyMethodReturn(&persistence.Persistence{}, "Save", nil)
		s.handleMessage(c)
		r.Equal(http.StatusOK, w.Code)
	})
}

func TestHttpServer_queryStateLogByID(t *testing.T) {
	r := require.New(t)

	s := &httpServer{}

	t.Run("FailedToFetchMessage", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", nil, errors.New(t.Name()))
		s.queryStateLogByID(c)
		r.Equal(http.StatusInternalServerError, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("FetchMessageZero", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = append(c.Params, gin.Param{Key: "id", Value: "some_message_id"})

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", []*persistence.Message{}, nil)
		s.queryStateLogByID(c)
		r.Equal(http.StatusOK, w.Code)

		actualResponse := &apitypes.QueryMessageStateLogRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Equal(&apitypes.QueryMessageStateLogRsp{
			MessageID: "some_message_id",
		}, actualResponse)
	})

	t.Run("FailedToAuthorized", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = append(c.Params, gin.Param{Key: "id", Value: "some_message_id"})
		c.Request, _ = http.NewRequest(http.MethodPost, "/", nil)

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", []*persistence.Message{
			{
				ClientID:       "clientID",
				ProjectID:      0,
				InternalTaskID: "internalTaskID",
			},
		}, nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "someClientID", true)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, errors.New(t.Name()))

		s.queryStateLogByID(c)
		r.Equal(http.StatusUnauthorized, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("ClientIDNotEqual", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = append(c.Params, gin.Param{Key: "id", Value: "some_message_id"})
		c.Request, _ = http.NewRequest(http.MethodPost, "/", nil)

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", []*persistence.Message{
			{
				ClientID:       "clientID",
				ProjectID:      0,
				InternalTaskID: "internalTaskID",
			},
		}, nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "someClientID", true)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, nil)

		s.queryStateLogByID(c)
		r.Equal(http.StatusUnauthorized, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, "unmatched client DID")
	})

	t.Run("FailedToFetchTask", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = append(c.Params, gin.Param{Key: "id", Value: "some_message_id"})
		c.Request, _ = http.NewRequest(http.MethodPost, "/", nil)

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", []*persistence.Message{
			{
				ClientID:       "clientID",
				ProjectID:      0,
				InternalTaskID: "internalTaskID",
			},
		}, nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "clientID", true)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, nil)
		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchTask", nil, errors.New(t.Name()))

		s.queryStateLogByID(c)
		r.Equal(http.StatusInternalServerError, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("FetchTaskZero", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = append(c.Params, gin.Param{Key: "id", Value: "some_message_id"})
		c.Request, _ = http.NewRequest(http.MethodPost, "/", nil)

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", []*persistence.Message{
			{
				ClientID:       "clientID",
				ProjectID:      0,
				InternalTaskID: "internalTaskID",
			},
		}, nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "clientID", true)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, nil)
		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchTask", []*persistence.Task{}, nil)

		s.queryStateLogByID(c)
		r.Equal(http.StatusInternalServerError, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, "cannot find task by internal task id")
	})

	t.Run("FailedToGetStateFromCoordinator", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = append(c.Params, gin.Param{Key: "id", Value: "some_message_id"})
		c.Request, _ = http.NewRequest(http.MethodPost, "/", nil)

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", []*persistence.Message{
			{
				ClientID:       "clientID",
				ProjectID:      0,
				InternalTaskID: "internalTaskID",
			},
		}, nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "clientID", true)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, nil)
		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchTask", []*persistence.Task{
			{
				Model: gorm.Model{
					ID:        0,
					CreatedAt: time.Time{},
				},
			},
		}, nil)
		p.ApplyFuncReturn(http.Get, nil, errors.New(t.Name()))

		s.queryStateLogByID(c)
		r.Equal(http.StatusInternalServerError, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("FailedToReadAll", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = append(c.Params, gin.Param{Key: "id", Value: "some_message_id"})
		c.Request, _ = http.NewRequest(http.MethodPost, "/", nil)

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", []*persistence.Message{
			{
				ClientID:       "clientID",
				ProjectID:      0,
				InternalTaskID: "internalTaskID",
			},
		}, nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "clientID", true)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, nil)
		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchTask", []*persistence.Task{
			{
				Model: gorm.Model{
					ID:        0,
					CreatedAt: time.Time{},
				},
			},
		}, nil)
		p.ApplyFuncReturn(http.Get, &http.Response{
			Body: &mockReadCloser{},
		}, nil)
		p.ApplyFuncReturn(io.ReadAll, nil, errors.New(t.Name()))

		s.queryStateLogByID(c)
		r.Equal(http.StatusInternalServerError, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("FailedToUnmarshal", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = append(c.Params, gin.Param{Key: "id", Value: "some_message_id"})
		c.Request, _ = http.NewRequest(http.MethodPost, "/", nil)

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", []*persistence.Message{
			{
				ClientID:       "clientID",
				ProjectID:      0,
				InternalTaskID: "internalTaskID",
			},
		}, nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "clientID", true)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, nil)
		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchTask", []*persistence.Task{
			{
				Model: gorm.Model{
					ID:        0,
					CreatedAt: time.Time{},
				},
			},
		}, nil)
		p.ApplyFuncReturn(http.Get, &http.Response{
			Body: &mockReadCloser{},
		}, nil)
		p.ApplyFuncReturn(io.ReadAll, []byte("body"), nil)
		p.ApplyFuncReturn(json.Unmarshal, errors.New(t.Name()))

		s.queryStateLogByID(c)
		r.Equal(http.StatusInternalServerError, w.Code)
	})

	t.Run("Success", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = append(c.Params, gin.Param{Key: "id", Value: "some_message_id"})
		c.Request, _ = http.NewRequest(http.MethodPost, "/", nil)

		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchMessage", []*persistence.Message{
			{
				ClientID:       "clientID",
				ProjectID:      0,
				InternalTaskID: "internalTaskID",
			},
		}, nil)
		p.ApplyFuncReturn(didvc.ClientIDFrom, "clientID", true)
		p.ApplyFuncReturn(clients.VerifyProjectPermissionByClientDID, nil)
		p.ApplyMethodReturn(&persistence.Persistence{}, "FetchTask", []*persistence.Task{
			{
				Model: gorm.Model{
					ID:        0,
					CreatedAt: time.Time{},
				},
			},
		}, nil)
		p.ApplyFuncReturn(http.Get, &http.Response{
			Body: &mockReadCloser{},
		}, nil)
		p.ApplyFuncReturn(io.ReadAll, []byte("body"), nil)
		p.ApplyFuncReturn(json.Unmarshal, nil)

		s.queryStateLogByID(c)
		r.Equal(http.StatusOK, w.Code)
	})
}

func TestHttpServer_issueJWTCredential(t *testing.T) {
	r := require.New(t)

	s := &httpServer{}

	t.Run("FailedToBindJson", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		p.ApplyMethodReturn(&gin.Context{}, "ShouldBindJSON", errors.New(t.Name()))
		s.issueJWTCredential(c)
		r.Equal(http.StatusBadRequest, w.Code)

		actualResponse := &apitypes.ErrRsp{}
		err := json.Unmarshal(w.Body.Bytes(), &actualResponse)
		r.NoError(err)
		r.Contains(actualResponse.Error, t.Name())
	})

	t.Run("FailedToIssueCredential", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		p.ApplyMethodReturn(&gin.Context{}, "ShouldBindJSON", nil)
		p.ApplyFuncReturn(didvc.IssueCredential, nil, errors.New(t.Name()))
		s.issueJWTCredential(c)
		r.Equal(http.StatusInternalServerError, w.Code)
		r.Contains(string(w.Body.Bytes()), t.Name())
	})

	t.Run("Success", func(t *testing.T) {
		p := NewPatches()
		defer p.Reset()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		p.ApplyMethodReturn(&gin.Context{}, "ShouldBindJSON", nil)
		p.ApplyFuncReturn(didvc.IssueCredential, &apis.IssueTokenRsp{}, nil)
		s.issueJWTCredential(c)
		r.Equal(http.StatusOK, w.Code)
	})
}

type mockReadCloser struct{}

func (m *mockReadCloser) Read(p []byte) (n int, err error) {
	return 0, nil
}

func (m *mockReadCloser) Close() error {
	return nil
}