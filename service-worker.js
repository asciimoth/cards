const CACHE_NAME = 'cardapp-v1';
const APP_SHELL = [
  '/',             // если у вас главная страница
  '/editor',       // маршрут редактирования, если он такой
  '/c',         // маршрут просмотра визитки
  '/static/style.css',
  'static/manifest.json',
  '/icons/icon-192.png',
  '/icons/icon-512.png',
  'https://cdn.jsdelivr.net/npm/cropperjs@1.5.13/dist/cropper.min.css',
  'https://cdn.jsdelivr.net/npm/cropperjs@1.5.13/dist/cropper.min.js'
];

self.addEventListener('install', evt => {
  evt.waitUntil(
    caches.open(CACHE_NAME)
      .then(cache => cache.addAll(APP_SHELL))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', evt => {
  evt.waitUntil(
    caches.keys()
      .then(keys => Promise.all(
        keys.filter(key => key !== CACHE_NAME)
            .map(key => caches.delete(key))
      ))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', evt => {
  const req = evt.request;
  if (req.method !== 'GET') return;    

  evt.respondWith(
    caches.match(req)
      .then(cached => cached || fetch(req).then(res => {

        if (res.ok) {
          const copy = res.clone();
          caches.open(CACHE_NAME).then(cache => cache.put(req, copy));
        }
        return res;
      }).catch(() => {

        if (req.mode === 'navigate') {
          return caches.match('/offline.html');
        }
      }))
  );
});
