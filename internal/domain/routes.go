package domain

// WorkerTaskPath is the path where the worker receives tasks.
// Both the enqueuer (tasks.Config.WorkerPath) and the receiver (internal/server's route
// registration) use it. Holding two copies still builds when only one changes, and every enqueued
// task then 404s. gcp-kit's tasks appends it to the service URL and normalises it.
const WorkerTaskPath = "/tasks/generate"
