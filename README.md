# TimescaleDB Benchmark Tool

# Dependencies
 * Docker >= 20.10.8
 * Docker Compose >= v2.0.0-rc.3

# Running the program

Running `docker-compose up` will run the program using queries from the `query_params.csv` file
```
docker-compose up
```

An example of running the tool with a different input file and worker count:
```
docker-compose up timescale -d 
docker-compose run -v ./other_params.csv:/other_params.csv tool -file other_params.csv -workers 4
```

An example of running the tool with input from stdin
```
docker-compose up timescale -d 
docker-compose run tool -file -
```
