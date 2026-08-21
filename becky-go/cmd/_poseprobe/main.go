package main

import (
	"fmt"
	"os"
	"strconv"

	"becky-go/internal/config"
	"becky-go/internal/crop"
)

func main() {
	cfg := config.Load()
	s, _ := strconv.ParseFloat(os.Args[2], 64)
	e, _ := strconv.ParseFloat(os.Args[3], 64)
	p, err := crop.Run(cfg, crop.Options{Video: os.Args[1], Start: s, End: e,
		Aspect: "1080:1920", FPS: 0, Model: cfg.PoseModel})
	if err != nil {
		fmt.Println("ERR", err)
		return
	}
	fmt.Printf("[%.2f,%.2f] sampled=%d found=%d coverage=%.3f longestGap=%.2f rects=%d\n",
		s, e, p.Sampled, p.Found, p.Coverage(), p.LongestGap, len(p.Rects))
}
