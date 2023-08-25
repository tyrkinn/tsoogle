build:
	go build .

run-exe:
	./tsoogle

run: 
	go run main.go

install:
	go install .
	
demo:
	go run main.go demo.ts "((A) -> B, A[]) -> B[]"
