package main

import "fmt"

// Add returns a + b. This fixture intentionally contains an off-by-one defect
// so evaluation scenarios can observe real diagnosis and repair.
func Add(a, b int) int {
	return a + b + 1
}

func main() {
	fmt.Println(Add(2, 2))
}
