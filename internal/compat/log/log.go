package log

import (
	"fmt"
	"log"
	"os"
)

func Infof(format string, v ...interface{}) {
	log.Printf("INFO: "+format, v...)
}

func Info(v ...interface{}) {
	log.Println(append([]interface{}{"INFO: "}, v...)...)
}

func Warningf(format string, v ...interface{}) {
	log.Printf("WARNING: "+format, v...)
}

func Warning(v ...interface{}) {
	log.Println(append([]interface{}{"WARNING: "}, v...)...)
}

func Errorf(format string, v ...interface{}) {
	log.Printf("ERROR: "+format, v...)
}

func Error(v ...interface{}) {
	log.Println(append([]interface{}{"ERROR: "}, v...)...)
}

func Fatalf(format string, v ...interface{}) {
	log.Printf("FATAL: "+format, v...)
	os.Exit(1)
}

func Fatal(v ...interface{}) {
	log.Println(append([]interface{}{"FATAL: "}, v...)...)
	os.Exit(1)
}

func Exitf(format string, v ...interface{}) {
	log.Printf("EXIT: "+format, v...)
	os.Exit(1)
}

func Exit(v ...interface{}) {
	log.Println(append([]interface{}{"EXIT: "}, v...)...)
	os.Exit(1)
}
