package main

import (
	"github.com/flwyd/adif-multitool/adif"
	"os"
)

func main() {
	println("testing")

	io := adif.NewADIIO()
	f, err := os.Open("./file.adi")
	if err != nil {
		panic(err)
	}

	log, err := io.Read(f)
	if err != nil {
		panic(err)
	}

	for record := range log.Records {
		println("record: %s", record)
	}
}
