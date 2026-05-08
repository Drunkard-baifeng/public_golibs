package main

import (
	"fmt"
	"time"

	"github.com/Drunkard-baifeng/public_golibs/filewriter"
)

func FileWriterCall() {
	// 1) 全局单例只设置一次目录
	if err := filewriter.SetDefaultDir("./data"); err != nil {
		panic(err)
	}
	defer filewriter.Close()

	// // 2) 正常写入（追加，不覆盖）
	// if err := filewriter.SaveLine("hello world", "demo"); err != nil {
	// 	panic(err)
	// }
	// if err := filewriter.SaveData("user1", "pass1", "token1", "remark1", "accounts"); err != nil {
	// 	panic(err)
	// }

	// // 3) 并发写入同一个文件
	// const goroutines = 50
	// const perGoroutine = 100

	// var wg sync.WaitGroup
	// errCh := make(chan error, goroutines)

	// wg.Add(goroutines)
	// for g := 0; g < goroutines; g++ {
	// 	go func(gid int) {
	// 		defer wg.Done()
	// 		for i := 0; i < perGoroutine; i++ {
	// 			line := fmt.Sprintf("g=%d,i=%d", gid, i)
	// 			if err := filewriter.SaveLine(line, "concurrent"); err != nil {
	// 				errCh <- err
	// 				return
	// 			}
	// 		}
	// 	}(g)
	// }

	// wg.Wait()
	// close(errCh)

	// if err, ok := <-errCh; ok {
	// 	panic(err)
	// }
	start := time.Now()
	for i := 0; i < 10000; i++ {
		filewriter.SaveLine(fmt.Sprintf("i = %d", i), "demo2")
	}
	end := time.Now()
	fmt.Println("time =", end.Sub(start))

	start = time.Now()
	for i := 0; i < 10000; i++ {
		filewriter.SaveData("user1", "pass1", "token1", "remark1", "demo3")
	}
	end = time.Now()
	fmt.Println("time =", end.Sub(start))

	// fmt.Println("done")
	// fmt.Println("output dir: ./data")
	// fmt.Println("files: demo.txt, accounts.txt, concurrent.txt")
}
