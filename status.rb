#!/usr/bin/env ruby

require 'net/http'
require 'json'
require 'uri'
require 'yaml'
require 'date'

token = ENV['SLACK_TOKEN']
config_file = ARGV[0] || 'status.yaml'

unless token
  puts 'Error: SLACK_TOKEN environment variable not set'
  exit 1
end

unless File.exist?(config_file)
  puts "Error: Config file '#{config_file}' not found"
  exit 1
end

config = YAML.load_file(config_file)
today = Date.today.strftime('%A').downcase

config.each do |user_id, schedule|
  status_config = schedule[today]
  next unless status_config

  uri = URI('https://slack.com/api/users.profile.set')
  http = Net::HTTP.new(uri.host, uri.port)
  http.use_ssl = true

  request = Net::HTTP::Post.new(uri)
  request['Authorization'] = "Bearer #{token}"
  request['Content-Type'] = 'application/json'

  profile = {
    status_text: status_config['text'],
    status_emoji: status_config['emoji']
  }

  request.body = { profile: profile, user: user_id }.to_json

  response = http.request(request)
  result = JSON.parse(response.body)

  if result['ok']
    puts "#{user_id}: Status set to '#{status_config['text']}' #{status_config['emoji']}"
  else
    puts "#{user_id}: Error - #{result['error']}"
  end
end
