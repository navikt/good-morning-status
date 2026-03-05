FROM ruby:3.4 AS builder

RUN gem install bundler

# throw errors if Gemfile has been modified since Gemfile.lock
RUN bundle config --global frozen 1

WORKDIR /app

ENV BUNDLE_WITHOUT="development:test" \
    BUNDLE_DEPLOYMENT="true"

COPY Gemfile Gemfile.lock ./
RUN bundle install

COPY config.ru slack_bot.rb ./
COPY config/ config/
COPY lib/ lib/

ENTRYPOINT ["bundler", "exec", "puma", "-C", "config/puma.rb"]
