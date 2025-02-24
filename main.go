package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

func init() {
	// Загружаем переменные окружения из файла .env
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}
}

var ( // груповая деклорация нескольких переменных
	requestLimit = 5 // Запросов в секунду
	totalStats   = struct {
		TotalPositive int `json:"total_positive"` // поля этой структуры аннотированны тэгами json, которые указывают, как поля будут сериализоваться в формате json
		TotalNegative int `json:"total_negative"`
	}{0, 0} // инициализация полей структуры нулюми
	clientStats = make(map[string]map[int]int)
	mu          sync.Mutex // мьютекс
)

func rateLimiter(next http.Handler) http.Handler {
	semaphore := make(chan struct{}, requestLimit)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case semaphore <- struct{}{}:
			defer func() { <-semaphore }()
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests) // уникальное сообщение о том что лимит превышен
		}
	})
}

func requestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost { // если это POST запрос

		statusCodes := []int{http.StatusOK, http.StatusOK, http.StatusOK, http.StatusOK, http.StatusOK,
			http.StatusAccepted, http.StatusAccepted, http.StatusAccepted, http.StatusAccepted, http.StatusAccepted,
			http.StatusBadRequest, http.StatusInternalServerError} // срез содержащий статусы

		status := statusCodes[rand.Intn(len(statusCodes))] // статус выбирается из среза случайным образом. rand.Intn(len(statusCodes)) - генерирует число от 0 до длинны среза
		clientID := r.Header.Get("Client-ID")              // получение заголовка из запроса

		mu.Lock() // блокировка мьтекс
		if _, exists := clientStats[clientID]; !exists {
			clientStats[clientID] = make(map[int]int) // если мапы еще нет, то создается новая
		}
		clientStats[clientID][status]++ // статус инкрементируется в мапе для этого клиента
		if status == http.StatusOK || status == http.StatusAccepted {
			totalStats.TotalPositive++ // увилечение в структуре счетчика с позитивными запросами
		} else {
			totalStats.TotalNegative++ // увеличение с негативными запросами
		}
		mu.Unlock() // разблокировка мьютекса

		w.WriteHeader(status)                    // устанавливает случайно выбранный статут
		w.Write([]byte(http.StatusText(status))) // записывает текстовое описание статуса http.StatusText - возвращает строку с текстом, соответствующим HTTP-статусу
	} else if r.Method == http.MethodGet { // если это GET запрос
		clientID := r.URL.Query().Get("client_id")
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json") // указание, что ответ будет в json
		if clientID == "" {
			// Если client_id не указан, возвращаем статистику для всех клиентов
			json.NewEncoder(w).Encode(clientStats) // NewEncoder - сереализация структуры в формат json
		} else {
			// Если client_id указан, возвращаем статистику для конкретного клиента
			if stats, exists := clientStats[clientID]; exists {
				json.NewEncoder(w).Encode(stats)
			} else {
				http.Error(w, "Client not found", http.StatusNotFound)
			}
		}
		// w.Header().Set("Content-Type", "application/json") // указание, что ответ будет в json
		// json.NewEncoder(w).Encode(totalStats)              // сереализация структуры в формат json
	}
}

func startServer(port string) {
	// port := os.Getenv("SERVER_PORT") // получает занчение указанной переменной из окружения
	// if port == "" {                  // если строка пустая, то устанавливается порт по умелчанию
	// 	port = "8080"
	// }
	http.Handle("/", rateLimiter(http.HandlerFunc(requestHandler))) // любой запрос на сервер обрабатывается переданной функцией
	log.Println("Server started on port", port)
	log.Fatal(http.ListenAndServe(":"+port, nil)) // ЗАПУСК СЕРВЕРА, к номеру порта добавляется :
}

func client(clientID string, port string) {
	client := &http.Client{}
	rateLimiter := time.Tick(200 * time.Millisecond) // ограничение частоты запросов,
	// var stats = make(map[int]int)                    // карта с подсчетом количества статусов ответов
	var (
		stats = make(map[int]int) // общая статистика для всех горутин
		mu    sync.Mutex          // для синхронизации доступа к stats
	)

	for i := 0; i < 2; i++ { // Запускаем два воркера
		go func(workerID int) {
			for i := 0; i < 100; i++ {
				<-rateLimiter // блокирует выполнение на этой строке, пока не поступит сигнал из канала rateLimiter
				// код внутри функции будет выполняться в новой горутине
				req, _ := http.NewRequest("POST", "http://localhost:"+port, nil) // новый POST запрос
				req.Header.Set("Client-ID", clientID)                            // установка заголовка
				resp, err := client.Do(req)                                      // отправляет запрос req, client - ранее созданный экземпляр http.Clien
				if err != nil {
					log.Println("Error:", err) // Ошибка при отправке запроса
					return                     // Выполнение горутин прекращается
				}
				mu.Lock()
				defer resp.Body.Close()  // отложенный вызов закрытия
				stats[resp.StatusCode]++ // увеличение каунтера для статуса ответа в мапе
				mu.Unlock()
			}
		}(i)
	}
	time.Sleep(20 * time.Second)                          // Ждем завершения всех горутин
	log.Printf("Client %s Stats: %+v\n", clientID, stats) // вывод статистики для каждого клиента
}

func healthChecker(port string) { // третий клиент, который каждые 5 секунд проверяет состояние сервера
	client := &http.Client{}
	for { // бесконечный цикл
		time.Sleep(5 * time.Second)                         // ожидание 5 секунд
		resp, err := client.Get("http://localhost:" + port) // отправка GET запроса на сервер
		if err != nil {
			log.Println("Server unavailable") // если есть ошибка, вывод о недоступности сервера
		} else {
			log.Println("Server status:", resp.StatusCode) // иначе вывод статуса сервера
		}
	}
}

func main() {
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080" // номер порта по умолчанию, если .env отсутсвует
	}
	go startServer(port)
	// синхронизация горутин через тайм слип
	time.Sleep(2 * time.Second) // Ждем запуска сервера

	go client("Client1", port)
	go client("Client2", port)
	go healthChecker(port)

	time.Sleep(60 * time.Second) // Даем время на выполнение
	// Чем больше обрабатывается запросов, тем больше нужно поставить время, чтобы все успели коректно обработаться и записаться в client_stats.json
	mu.Lock() // блокировка мьютекса, чтобы избежать одновременного измения переменной из разных горутин totalStats
	data, _ := json.MarshalIndent(clientStats, "", "  ")
	_ = ioutil.WriteFile("client_stats.json", data, 0644)
	mu.Unlock()
	log.Println("Results saved to stats.json")
	// Сервер продолжает работать для обработки GET запросов
	select {} // Бесконечный цикл, чтобы сервер не завершал работу
}
