// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

import (
	"path"
	"runtime"
)

func AudioTestFile(name string) string {
	_, filename, _, _ := runtime.Caller(1)
	dir := path.Dir(filename)
	playfile := path.Join(dir, "./testdata/files/"+name)
	return playfile

}
