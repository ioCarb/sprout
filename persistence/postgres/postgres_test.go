package postgres

import (
	"testing"

	. "github.com/agiledragon/gomonkey/v2"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/machinefi/sprout/task"
	"github.com/machinefi/sprout/testutil"
)

func TestPostgres_ProcessedTaskID(t *testing.T) {
	r := require.New(t)
	p := NewPatches()
	defer p.Reset()

	v := &Postgres{
		db: &gorm.DB{
			Error:     nil,
			Statement: &gorm.Statement{},
		},
	}

	testutil.GormDBWhere(p, v.db)
	testutil.GormDBOrder(p, v.db)

	t.Run("FailedToFindDB", func(t *testing.T) {
		p.ApplyMethodReturn(&gorm.DB{}, "First", &gorm.DB{Error: errors.New(t.Name())})
		_, err := v.ProcessedTaskID(1)
		r.ErrorContains(err, t.Name())
	})
	t.Run("RecordNotFound", func(t *testing.T) {
		p.ApplyMethodReturn(&gorm.DB{}, "First", &gorm.DB{Error: gorm.ErrRecordNotFound})
		res, err := v.ProcessedTaskID(1)
		r.NoError(err)
		r.Equal(uint64(0), res)
	})

	testutil.GormDBFirst(p, &projectProcessedTask{}, v.db)

	t.Run("Success", func(t *testing.T) {
		res, err := v.ProcessedTaskID(1)
		r.NotNil(res)
		r.NoError(err)
	})
}

func TestPostgres_UpsertProcessedTask(t *testing.T) {
	r := require.New(t)
	p := NewPatches()
	defer p.Reset()

	db := &gorm.DB{
		Error:     nil,
		Statement: &gorm.Statement{},
	}
	p.ApplyFuncReturn(New, &Postgres{db: db}, nil)
	v, err := New("any")
	r.NoError(err)
	r.NotNil(v)

	t.Run("FailedToUpsertProcessedTask", func(t *testing.T) {
		ndb := *db
		ndb.Error = errors.New(t.Name())
		testutil.GormDBClauses(p, &ndb)
		testutil.GormDBCreate(p, nil, &ndb)

		err := v.UpsertProcessedTask(1, 1)
		r.ErrorContains(err, t.Name())
	})
	testutil.GormDBClauses(p, db)
	testutil.GormDBCreate(p, nil, db)

	t.Run("Success", func(t *testing.T) {
		err := v.UpsertProcessedTask(1, 1)
		r.NoError(err)
	})
}

func TestPostgres_Create(t *testing.T) {
	r := require.New(t)
	p := NewPatches()
	defer p.Reset()

	db := &gorm.DB{
		Error:     nil,
		Statement: &gorm.Statement{},
	}
	p = p.ApplyFuncReturn(New, &Postgres{db: db}, nil)
	v, err := New("any")
	r.NoError(err)
	r.NotNil(v)

	t.Run("FailedToCreateTaskStateLog", func(t *testing.T) {
		ndb := *db
		ndb.Error = errors.New(t.Name())
		p = testutil.GormDBCreate(p, nil, &ndb)

		err := v.Create(&task.StateLog{}, &task.Task{})
		r.ErrorContains(err, t.Name())
	})
	p = testutil.GormDBCreate(p, nil, db)

	t.Run("Success", func(t *testing.T) {
		err := v.Create(&task.StateLog{}, &task.Task{})
		r.NoError(err)
	})
}

func TestPostgres_Fetch(t *testing.T) {
	r := require.New(t)
	p := NewPatches()
	defer p.Reset()

	v := &Postgres{
		db: &gorm.DB{
			Error:     nil,
			Statement: &gorm.Statement{},
		},
	}

	p = testutil.GormDBWhere(p, v.db)
	p = testutil.GormDBOrder(p, v.db)

	t.Run("FailedToFindDB", func(t *testing.T) {
		p = p.ApplyMethodReturn(&gorm.DB{}, "Find", &gorm.DB{Error: errors.New(t.Name())})
		_, err := v.Fetch(1, 1)
		r.ErrorContains(err, t.Name())
	})

	p = testutil.GormDBFind(p, &([]*taskStateLog{{}, {}, {}}), v.db)

	t.Run("Success", func(t *testing.T) {
		_task, err := v.Fetch(1, 1)
		r.NotNil(_task)
		r.NoError(err)
	})
}

func TestNewPostgres(t *testing.T) {
	r := require.New(t)
	p := NewPatches()
	defer p.Reset()
	d := &gorm.DB{}

	t.Run("FailedToOpenDSN", func(t *testing.T) {
		p = testutil.GormOpen(p, nil, errors.New(t.Name()))
		v, err := New("any")
		r.Nil(v)
		r.ErrorContains(err, t.Name())
	})
	t.Run("FailedToMigrate", func(t *testing.T) {
		p = testutil.GormOpen(p, d, nil)
		p = testutil.GormDBAutoMigrate(p, errors.New(t.Name()))
		v, err := New("any")
		r.Nil(v)
		r.ErrorContains(err, t.Name())
	})
	t.Run("Success", func(t *testing.T) {
		p = testutil.GormOpen(p, d, nil)
		p = testutil.GormDBAutoMigrate(p, nil)
		v, err := New("any")
		r.NotNil(v)
		r.NoError(err)
	})
}
