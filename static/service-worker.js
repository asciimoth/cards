const CACHE_NAME = "business-card-cache-v3";
const CACHE_URLS = [
  "/static/style.css",
  "/static/collapse.js",
  //"/static/default-avatar.png",
  // "/static/icon-192.png",
  // "/static/icon-512.png",
];

// Установка и кэширование основных ресурсов
self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE_NAME)
      .then((cache) => cache.addAll(CACHE_URLS))
      .then(() => self.skipWaiting()),
  );
});

// Активация - очистка старых кэшей
self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((cacheNames) => {
        return Promise.all(
          cacheNames.map((cache) => {
            if (cache !== CACHE_NAME) {
              return caches.delete(cache);
            }
          }),
        );
      })
      .then(() => self.clients.claim()),
  );
});

// Стратегия: Network First, затем Cache с динамическим fallback
self.addEventListener("fetch", (event) => {
  // Для API и динамических данных - только сеть
  if (event.request.url.includes("/api/")) {
    return;
  }

  // Для статических ресурсов: Cache First
  if (CACHE_URLS.some((url) => event.request.url.includes(url))) {
    event.respondWith(
      caches
        .match(event.request)
        .then((cached) => cached || fetch(event.request)),
    );
    return;
  }

  // Для HTML-страниц: Network First + динамический fallback
  if (event.request.mode === "navigate") {
    event.respondWith(
      fetch(event.request)
        .then((networkResponse) => {
          // Обновляем кэш при успешном запросе
          const clone = networkResponse.clone();
          caches
            .open(CACHE_NAME)
            .then((cache) => cache.put(event.request, clone));
          return networkResponse;
        })
        .catch(() => {
          // Генерируем офлайн-страницу динамически
          return new Response(
            `
                        <!DOCTYPE html>
                        <html>
                        <head>
                            <title>{{ T "OfflineTitle" .Lang }}</title>
                            <meta charset="UTF-8">
                            <meta name="viewport" content="width=device-width, initial-scale=1">
                            <style>
                                body {
                                    font-family: Arial, sans-serif;
                                    text-align: center;
                                    padding: 20px;
                                    background-color: #f0f0f0;
                                }
                                .offline-container {
                                    max-width: 500px;
                                    margin: 50px auto;
                                    padding: 30px;
                                    background: white;
                                    border-radius: 10px;
                                    box-shadow: 0 2px 10px rgba(0,0,0,0.1);
                                }
                            </style>
                        </head>
                        <body>
                            <div class="offline-container">
                                <h1>{{ T "OfflineHeader" .Lang }}</h1>
                                <p>{{ T "OfflineMessage" .Lang }}</p>
                                <button onclick="location.reload()">{{ T "RetryButton" .Lang }}</button>
                            </div>
                        </body>
                        </html>
                    `,
            {
              headers: { "Content-Type": "text/html" },
            },
          );
        }),
    );
    return;
  }

  // Для всего остального: Network First
  event.respondWith(
    fetch(event.request).catch(() => caches.match(event.request)),
  );
});
