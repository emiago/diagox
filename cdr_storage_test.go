// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCDRMemory(t *testing.T) {

	s := NewCDRMemoryStorage()

	for i := 0; i < 10000; i++ {
		s.CDRWrite(context.TODO(), CDR{StartTime: time.Now()})
	}

	cdr := CDR{StartTime: time.Now(), StartTimeFormated: time.Now().String()}
	s.CDRWrite(context.TODO(), cdr)

	buf := make([]CDR, 1)
	n, err := s.CDRRead(context.TODO(), buf, CDRReadOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.EqualValues(t, cdr, buf[0])
}
