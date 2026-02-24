# SentinelStream 🚀

A high-performance, distributed real-time system monitoring engine built with Go and Docker.

## 🛠 Tech Stack
- **Language:** Go (Golang)
- **Message Broker:** Redis (Pub/Sub)
- **Database:** InfluxDB (Time-Series)
- **Infrastructure:** Docker & Docker Compose

## 🏗 Architecture
- **Agent:** A lightweight Go service that scrapes system metrics (CPU/RAM) and streams them via Redis.
- **Server:** A concurrent Go consumer that processes the stream and persists data to InfluxDB.
- **Dashboard:** Real-time visualization via InfluxDB dashboards.

## 🌟 Key Engineering Features
- **Graceful Shutdown:** Implemented OS signal handling to ensure zero data loss during service restarts.
- **Concurrency:** Utilized Go routines and channels for non-blocking data processing.
- **Containerization:** Fully orchestrated microservice environment using Docker Compose.
- **Clean Architecture:** Separated concerns into `cmd` (entry points) and `internal` (business logic) packages.

## 🚀 How to Run
1. Clone the repo.
2. Run `docker compose up --build`.
3. Access the dashboard at `http://localhost:8086`.
