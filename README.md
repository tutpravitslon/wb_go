```
go mod init wb_go
go get github.com/joho/godotenv
touch .env
echo "SERVER_PORT=8080" > .env
go run main.go
```

