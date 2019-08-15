package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/lindb/lindb/models"
	"github.com/lindb/lindb/pkg/state"
)

func TestDatabaseService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := state.NewMockRepository(ctrl)

	db := NewDatabaseService(repo)

	database := models.Database{
		Name: "test",
		Clusters: []models.DatabaseCluster{
			{
				Name:          "test",
				NumOfShard:    12,
				ReplicaFactor: 3,
			},
		},
	}
	data, _ := json.Marshal(&database)

	repo.EXPECT().Put(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	err := db.Save(&database)
	if err != nil {
		t.Fatal(err)
	}

	repo.EXPECT().Get(gomock.Any(), gomock.Any()).Return(data, nil)
	database2, _ := db.Get("test")
	assert.Equal(t, database, *database2)
	repo.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, state.ErrNotExist)
	database2, err = db.Get("test_not_exist")
	assert.Equal(t, state.ErrNotExist, err)
	assert.Nil(t, database2)
	database2, err = db.Get("")
	assert.NotNil(t, err)
	assert.Nil(t, database2)

	// json unmarshal error
	repo.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]byte{1, 1, 1}, nil)
	database2, err = db.Get("json_unmarshal_err")
	assert.NotNil(t, err)
	assert.Nil(t, database2)

	// test create database error
	err = db.Save(&models.Database{})
	assert.NotNil(t, err)

	err = db.Save(&models.Database{Name: "test"})
	assert.NotNil(t, err)

	err = db.Save(&models.Database{
		Name: "test",
		Clusters: []models.DatabaseCluster{
			{
				NumOfShard:    12,
				ReplicaFactor: 3,
			},
		},
	})
	assert.NotNil(t, err)

	err = db.Save(&models.Database{
		Name: "test",
		Clusters: []models.DatabaseCluster{
			{
				Name:          "test",
				ReplicaFactor: 3,
			},
		},
	})
	assert.NotNil(t, err)

	err = db.Save(&models.Database{
		Name: "test",
		Clusters: []models.DatabaseCluster{
			{
				Name:       "test",
				NumOfShard: 3,
			},
		},
	})
	assert.NotNil(t, err)
}

func TestDatabaseService_List(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := state.NewMockRepository(ctrl)
	db := NewDatabaseService(repo)

	repo.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("err"))
	list, err := db.List()
	assert.NotNil(t, err)
	assert.Nil(t, list)

	repo.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil)
	list, err = db.List()
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 0, len(list))
	database := models.Database{
		Name: "test",
		Clusters: []models.DatabaseCluster{
			{
				Name:          "test",
				NumOfShard:    12,
				ReplicaFactor: 3,
			},
		},
	}
	data, _ := json.Marshal(&database)
	repo.EXPECT().List(gomock.Any(), gomock.Any()).Return([][]byte{data, {1, 2, 4}}, nil)
	list, err = db.List()
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 1, len(list))
	assert.Equal(t, database, *(list[0]))
}
