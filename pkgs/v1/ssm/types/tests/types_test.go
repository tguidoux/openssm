package types_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

func TestTimestampJSON(t *testing.T) {
	ts := types.Timestamp{Time: time.Unix(1700000000, 123_000_000).UTC()}
	data, err := json.Marshal(ts)
	require.NoError(t, err)
	assert.Equal(t, "1700000000.123", string(data))

	var back types.Timestamp
	require.NoError(t, json.Unmarshal(data, &back))
	assert.True(t, ts.Equal(back.Time))

	require.NoError(t, json.Unmarshal([]byte(`"2024-01-02T03:04:05Z"`), &back))
	assert.Equal(t, 2024, back.Year())
	assert.Nil(t, types.NewTimestamp(time.Time{}))
}

func TestAsSSMErrorWrapsUnknown(t *testing.T) {
	e := types.AsSSMError(errors.New("boom"))
	assert.Equal(t, types.ErrInternalServerError, e.Code())
	assert.Equal(t, 500, e.HTTPStatusCode())

	wrapped := types.AsSSMError(errors.Join(errors.New("ctx"), types.NewParameterNotFound("/x")))
	assert.Equal(t, types.ErrParameterNotFound, wrapped.Code())
}

func TestPaginate(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	page, next, err := types.Paginate(items, "", 2, 10, 10)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, page)
	page, next, err = types.Paginate(items, next, 2, 10, 10)
	require.NoError(t, err)
	assert.Equal(t, []int{3, 4}, page)
	page, next, err = types.Paginate(items, next, 2, 10, 10)
	require.NoError(t, err)
	assert.Equal(t, []int{5}, page)
	assert.Empty(t, next)

	_, _, err = types.Paginate(items, "", 11, 10, 10)
	assert.True(t, types.IsCode(err, types.ErrValidation))
	_, _, err = types.Paginate(items, types.EncodeNextToken(99), 1, 10, 10)
	assert.True(t, types.IsCode(err, types.ErrInvalidNextToken))
}

func TestARNs(t *testing.T) {
	a := types.Account{AccountID: "1", Region: "r"}
	assert.Equal(t, "arn:aws:ssm:r:1:parameter/a/b", a.ParameterARN("/a/b"))
	assert.Equal(t, "arn:aws:ssm:r:1:parameter/flat", a.ParameterARN("flat"))
	assert.Equal(t, "arn:aws:ssm:r:1:document/Doc", a.DocumentARN("Doc"))
}
