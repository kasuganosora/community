package utils

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"unsafe"
)

func TestDeepCopy(t *testing.T) {
	type TestStruct struct {
		A             int
		B             string
		C             []int
		D             map[string]int
		E             *int
		unexported    int
		unexportedPtr *int
	}

	source := TestStruct{
		A:             1,
		B:             "2",
		C:             []int{3, 4},
		D:             map[string]int{"5": 6},
		E:             new(int),
		unexported:    8,
		unexportedPtr: new(int),
	}

	ptrVal := int(7)
	source.E = &ptrVal
	source.unexportedPtr = &ptrVal

	var target TestStruct

	err := DeepCopy(source, &target)
	require.NoError(t, err)
	assert.Equal(t, source, target)
	assert.NotEqual(t, unsafe.Pointer(&source), unsafe.Pointer(&target)) //nolint:gosec // G103: reviewed and safe

	type TestMap map[string]*TestStruct

	sourceMap := TestMap{
		"1": &source,
	}

	var targetMap TestMap

	err = DeepCopy(sourceMap, &targetMap)
	require.NoError(t, err)
	assert.Equal(t, sourceMap, targetMap)

	type TestSlice []*TestStruct

	sourceSlice := TestSlice{&source}

	var targetSlice TestSlice

	err = DeepCopy(sourceSlice, &targetSlice)
	require.NoError(t, err)
	assert.Equal(t, sourceSlice, targetSlice)

	type NestedTestStruct struct {
		TestStruct

		GG int
	}

	nestedSource := NestedTestStruct{
		TestStruct: source,
		GG:         9,
	}

	var nestedTarget NestedTestStruct

	err = DeepCopy(nestedSource, &nestedTarget)
	require.NoError(t, err)
	assert.Equal(t, nestedSource, nestedTarget)

	fmt.Println("source:", nestedTarget.A, nestedTarget.B, nestedTarget.C, nestedTarget.D, *nestedTarget.E, nestedTarget.GG)
}
