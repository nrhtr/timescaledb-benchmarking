package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
)

var dbPool *pgxpool.Pool

const (
	csvHostnameField  = 0
	csvStartField     = 1
	csvEndField       = 2
	dbConnectAttempts = 5
	dbConnectDelay    = 10
)

type task struct {
	hostname string
	start    string
	end      string
}

type benchResult struct {
	queryTime int64
}

func worker(id int, in <-chan task, out chan<- benchResult) {
	log.Printf("[INFO] Starting worker %d\n", id)

	for q := range in {
		var bucket time.Time
		var minCpu float64
		var maxCpu float64

		t0 := time.Now()
		err := dbPool.QueryRow(context.Background(),
			`SELECT time_bucket('1 minutes', ts) AS minute,
		MIN(usage) as minCpu,
		MAX(usage) as maxCpu
		FROM cpu_usage
		WHERE host=$1 AND ts >= $2 AND ts <= $3
		GROUP BY host, minute`, q.hostname, q.start, q.end).Scan(&bucket, &minCpu, &maxCpu)
		if err != nil {
			log.Printf("[ERROR] Failed retrieving row: %s\n", err.Error())
			continue
		}
		t1 := time.Now()
		delta := t1.Sub(t0).Microseconds()

		bench := benchResult{
			queryTime: delta,
		}

		out <- bench
	}
}

func processCSV(f io.Reader, numWorkers int, results chan<- benchResult, done chan<- bool) {
	cr := csv.NewReader(f)

	var wg sync.WaitGroup
	workers := make([]chan task, numWorkers)

	// Initialise channels and start workers
	for w := range workers {
		workers[w] = make(chan task)
		wg.Add(1)
		// Pass 'w' in to ensure each closure binds to new value of 'w'
		go func(w int) {
			defer wg.Done()
			worker(w, workers[w], results)
		}(w)
	}

	// Skip header
	_, err := cr.Read()
	if err != nil {
		log.Fatalf("[ERROR] Error when reading CSV header: %s\n", err.Error())
	}

	for {
		record, err := cr.Read()
		if err == io.EOF {
			log.Print("[INFO] Reached end of file\n")
			break
		} else if err != nil {
			log.Fatalf("[ERROR] Failed parsing CSV file: %s", err.Error())
		}

		hostname := record[csvHostnameField]
		start := record[csvStartField]
		end := record[csvEndField]

		// Select which worker to use for hostname
		h := fnv.New32a()
		h.Write([]byte(hostname))
		chosenWorker := int(h.Sum32()) % numWorkers

		t := task{
			hostname: hostname,
			start:    start,
			end:      end,
		}

		workers[chosenWorker] <- t
	}

	// Tell workers to shutdown
	for w := range workers {
		close(workers[w])
	}

	// Await completion of all workers and signal main goroutine
	log.Print("[INFO] Waiting for workers to shutdown...\n")
	wg.Wait()
	done <- true
}

func main() {
	fileName := flag.String("file", "-", "input filename (csv)")
	numWorkers := flag.Int("workers", 2, "number of workers")
	flag.Parse()

	dbHost := os.Getenv("POSTGRES_HOST")
	if dbHost == "" {
		log.Fatal("[ERROR] must set POSTGRES_HOST environment variable\n")
	}

	dbUser := os.Getenv("POSTGRES_USER")
	if dbUser == "" {
		log.Fatal("[ERROR] must set POSTGRES_USER environment variable\n")
	}

	dbPassword := os.Getenv("POSTGRES_PASSWORD")
	if dbPassword == "" {
		log.Fatal("[ERROR] must set POSTGRES_PASSWORD environment variable\n")
	}

	dbDatabase := os.Getenv("POSTGRES_DATABASE")
	if dbDatabase == "" {
		log.Fatal("[ERROR] must set POSTGRES_DATABASE environment variable\n")
	}

	dbUrl := fmt.Sprintf("postgres://%s:%s@%s/%s", dbUser, dbPassword, dbHost, dbDatabase)

	if *numWorkers < 1 {
		log.Fatal("[ERROR] workers must be at least 1\n")
	}

	var err error
	var attempt int
	for attempt = 0; attempt < dbConnectAttempts; attempt++ {
		log.Printf("[INFO] Connecting to database [attempt %d] ...\n", attempt)
		dbPool, err = pgxpool.Connect(context.Background(), dbUrl)
		if err == nil {
			break
		}
		time.Sleep(dbConnectDelay * time.Second)
	}
	if err != nil {
		log.Fatalf("[ERROR] Unable to connect to %s after %d attempts: %s\n", dbUrl, attempt, err.Error())
	}

	results := make(chan benchResult)

	var f *os.File
	if *fileName == "-" {
		f = os.Stdin
	} else {
		var err error
		f, err = os.Open(*fileName)
		if err != nil {
			log.Fatalf("[ERROR] Error when opening file %s: %s", *fileName, err.Error())
		}
	}

	done := make(chan bool)
	go processCSV(f, *numWorkers, results, done)

	var queryTimes []int64

	// Values are in microseconds
	firstResult := <-results
	queryTimes = append(queryTimes, firstResult.queryTime)
	minQueryTime := firstResult.queryTime
	maxQueryTime := firstResult.queryTime
	medianQueryTime := firstResult.queryTime
	totalQueryTime := firstResult.queryTime

out:
	for {
		select {
		case r := <-results:
			queryTimes = append(queryTimes, r.queryTime)
			totalQueryTime += r.queryTime
			if r.queryTime < minQueryTime {
				minQueryTime = r.queryTime
			} else if r.queryTime > maxQueryTime {
				maxQueryTime = r.queryTime
			}
		case _ = <-done:
			log.Print("[INFO] Gathered all results\n")
			break out
		}
	}

	// Accumulating all results and then sorting is not
	// the most efficient, but makes calculating the median
	// value straightforward
	sort.Slice(queryTimes, func(i, j int) bool {
		return queryTimes[i] < queryTimes[j]
	})
	n := len(queryTimes)
	if n%2 == 0 {
		medianQueryTime = (queryTimes[n/2-1] + queryTimes[n/2]) / 2
	} else {
		medianQueryTime = queryTimes[n/2]
	}

	fmt.Printf("\n###########################\n")
	fmt.Printf("Number of queries: %d\n", len(queryTimes))
	fmt.Printf("Total query time:  %dms\n", totalQueryTime/1000)
	fmt.Printf("Min query time:    %dms\n", minQueryTime/1000)
	fmt.Printf("Max query time:    %dms\n", maxQueryTime/1000)
	fmt.Printf("Mean query time:   %dms\n", totalQueryTime/int64(len(queryTimes))/1000)
	fmt.Printf("Median query time: %dms\n", medianQueryTime/1000)
}
