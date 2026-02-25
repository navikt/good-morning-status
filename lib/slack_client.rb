# frozen_string_literal: true

require 'net/http'
require 'json'
require 'uri'
require 'time'

class SlackClient
  TIMEZONE = 'Europe/Oslo'

  def initialize(token)
    @token = token
  end

  def set_status(user_id, text, emoji)
    uri = URI('https://slack.com/api/users.profile.set')
    http = Net::HTTP.new(uri.host, uri.port)
    http.use_ssl = true

    request = Net::HTTP::Post.new(uri)
    request['Authorization'] = "Bearer #{@token}"
    request['Content-Type'] = 'application/json; charset=utf-8'

    profile = {
      status_text: text,
      status_emoji: emoji,
      status_expiration: end_of_today
    }

    request.body = { profile: profile, user: user_id }.to_json

    response = http.request(request)
    JSON.parse(response.body)
  end

  def send_dm(user_id, text)
    uri = URI('https://slack.com/api/chat.postMessage')
    http = Net::HTTP.new(uri.host, uri.port)
    http.use_ssl = true

    request = Net::HTTP::Post.new(uri)
    request['Authorization'] = "Bearer #{@token}"
    request['Content-Type'] = 'application/json; charset=utf-8'

    request.body = {
      channel: user_id,
      text: text,
      metadata: {
        event_type: 'status_set',
        event_payload: {}
      }
    }.to_json

    response = http.request(request)
    JSON.parse(response.body)
  end

  private

  def end_of_today
    now = Time.now.getlocal('+01:00')
    Time.new(now.year, now.month, now.day, 23, 59, 0, '+01:00').to_i
  end
end
