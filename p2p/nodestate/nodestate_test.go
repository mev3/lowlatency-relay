// Copyright 2020 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package nodestate

import (
	"errors"
	"fmt"
	"github.com/pioplat/pioplat-core/rlp"
	"reflect"
)

func testSetup(flagPersist []bool, fieldType []reflect.Type) (*Setup, []Flags, []Field) {
	setup := &Setup{}
	flags := make([]Flags, len(flagPersist))
	for i, persist := range flagPersist {
		if persist {
			flags[i] = setup.NewPersistentFlag(fmt.Sprintf("flag-%d", i))
		} else {
			flags[i] = setup.NewFlag(fmt.Sprintf("flag-%d", i))
		}
	}
	fields := make([]Field, len(fieldType))
	for i, ftype := range fieldType {
		switch ftype {
		case reflect.TypeOf(uint64(0)):
			fields[i] = setup.NewPersistentField(fmt.Sprintf("field-%d", i), ftype, uint64FieldEnc, uint64FieldDec)
		case reflect.TypeOf(""):
			fields[i] = setup.NewPersistentField(fmt.Sprintf("field-%d", i), ftype, stringFieldEnc, stringFieldDec)
		default:
			fields[i] = setup.NewField(fmt.Sprintf("field-%d", i), ftype)
		}
	}
	return setup, flags, fields
}

func uint64FieldEnc(field interface{}) ([]byte, error) {
	if u, ok := field.(uint64); ok {
		enc, err := rlp.EncodeToBytes(&u)
		return enc, err
	}
	return nil, errors.New("invalid field type")
}

func uint64FieldDec(enc []byte) (interface{}, error) {
	var u uint64
	err := rlp.DecodeBytes(enc, &u)
	return u, err
}

func stringFieldEnc(field interface{}) ([]byte, error) {
	if s, ok := field.(string); ok {
		return []byte(s), nil
	}
	return nil, errors.New("invalid field type")
}

func stringFieldDec(enc []byte) (interface{}, error) {
	return string(enc), nil
}
