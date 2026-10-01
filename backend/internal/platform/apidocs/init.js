// Запуск Swagger UI — отдельным файлом, а не inline: CSP страницы запрещает inline-скрипты.
window.ui = SwaggerUIBundle({
  url: '/docs/openapi.json',
  dom_id: '#swagger-ui',
  deepLinking: true,
  // токен из Authorize переживает перезагрузку страницы (localStorage этого браузера)
  persistAuthorization: true,
  displayRequestDuration: true,
  tryItOutEnabled: true,
  // без бейджа validator.swagger.io: контракт уходил бы на сторонний сервис
  validatorUrl: null,
});
