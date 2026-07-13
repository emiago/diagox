#!/bin/bash

# Get the number of CPU cores
num_cores=$(nproc)

# Calculate half the number of cores
half_cores=$((num_cores / 2))

# Function to perform a CPU-bound task
cpu_task() {
  while :; do :; done
}

# Trap SIGINT (Ctrl+C) to cleanly exit
trap 'echo "Stopping..."; kill 0; exit' SIGINT

# Run the CPU-bound task in the background for half the number of cores
for ((i = 0; i < half_cores; i++)); do
  (cpu_task) &  # Using parentheses to create a subshell for multithreading
done

# Wait for background jobs to finish (they won't, since they run indefinitely)
wait