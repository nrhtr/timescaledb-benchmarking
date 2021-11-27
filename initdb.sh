#!/bin/sh

psql -U postgres < /cpu_usage.sql
psql -U postgres -d homework -c "\COPY cpu_usage FROM /cpu_usage.csv CSV HEADER"
