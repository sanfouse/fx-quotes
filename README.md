# FX Quotes

Сервис для асинхронного получения валютных котировок из Frankfurter.

## Локальный запуск

Понадобятся Git и Docker с Docker Compose.

```sh
git clone https://github.com/sanfouse/fx-quotes.git
cd fx-quotes
cp .env.example .env
docker compose up --build -d
```

PostgreSQL и API запустятся автоматически, миграция применится при запуске. API доступен по адресу `http://localhost:3000`.

Настройки задаются в `.env`: таймауты — в формате `5s` или `250ms`, размеры — в байтах. `WORKER_LEASE` должен быть больше `WORKER_ATTEMPT_TIMEOUT`. После изменения настроек повторите `docker compose up --build -d`. При изменении `HTTP_PORT` используйте новый порт в запросах.

Отправить запрос на обновление курса:

```sh
curl -i http://localhost:3000/quotes \
  -H 'Content-Type: application/json' \
  -d '{"pair":"EUR/MXN"}'
```

В ответе придёт ID задачи. Подставьте его вместо `1`, чтобы получить результат:

```sh
curl http://localhost:3000/quotes/1
```

Последняя успешно полученная котировка:

```sh
curl -G http://localhost:3000/quotes/latest --data-urlencode 'pair=EUR/MXN'
```

Остановить сервис:

```sh
docker compose down
```

Данные сохраняются в Docker-томе между запусками.

## Тестовый стенд

Развернул стенд на `https://82.38.71.115`, чтобы можно было попробовать API без локальной установки. Запросы те же — замените `http://localhost:3000` на адрес стенда:

```sh
curl -i https://82.38.71.115/quotes \
  -H 'Content-Type: application/json' \
  -d '{"pair":"EUR/MXN"}'

curl -G https://82.38.71.115/quotes/latest --data-urlencode 'pair=EUR/MXN'
```
