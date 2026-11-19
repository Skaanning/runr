package main

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// mainEnv makes the test binary act like runr itself, e.g. as a session
// host started by the code under test (os.Executable).
const mainEnv = "RUNR_TEST_MAIN"

// sleepEnv makes the test binary a project's program: it prints the
// variable's value, then sleeps for a minute. Checked first: a host's
// projects inherit mainEnv.
const sleepEnv = "RUNR_TEST_SLEEP"

func TestMain(m *testing.M) {
	if text := os.Getenv(sleepEnv); text != "" {
		fmt.Println(text)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if os.Getenv(mainEnv) != "" {
		main()
		os.Exit(0)
	}
	testHelpers()
	os.Exit(m.Run())
}
