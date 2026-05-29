FROM php:8.2-cli

RUN apt-get update && apt-get install -y git unzip \
    && pecl install redis \
    && docker-php-ext-enable redis \
    && rm -rf /var/lib/apt/lists/*

COPY --from=composer:2 /usr/bin/composer /usr/bin/composer

WORKDIR /app
COPY composer.json ./
RUN composer install --no-dev --optimize-autoloader --no-interaction

COPY consumer.php ./

# Disable execution time limit for long-running consumer
CMD ["php", "-d", "max_execution_time=0", "consumer.php"]
