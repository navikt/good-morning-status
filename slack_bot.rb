#!/usr/bin/env ruby
# frozen_string_literal: true

require 'sinatra'
require 'json'
require 'net/http'
require 'date'
require_relative 'lib/valkey_client'
require_relative 'lib/slack_client'
require_relative 'lib/json_logger'

set :port, 4567
set :valkey, ValkeyClient.new
set :slack, SlackClient.new(ENV.fetch('SLACK_USER_TOKEN', nil), ENV.fetch('SLACK_BOT_TOKEN', nil))
set :logger, JsonLogger.new

post '/slack/interactions' do
  payload = JSON.parse(params['payload'])

  case payload['type']
  when 'view_submission'
    handle_form_submission(payload)
  when 'block_actions'
    handle_block_actions(payload)
  end
end

post '/slack/commands' do
  trigger_id = params['trigger_id']
  user_id = params['user_id']
  text = (params['text'] || '').strip

  if text == 'unsubscribe'
    settings.valkey.delete_schedule(user_id)
    content_type :json
    return { response_type: 'ephemeral', text: 'Du er nå avmeldt fra en god morgen.' }.to_json
  end

  schedule = settings.valkey.get_schedule(user_id)
  open_modal(trigger_id, schedule)

  status 200
  body ''
end

def open_modal(trigger_id, schedule = nil)
  uri = URI('https://slack.com/api/views.open')
  http = Net::HTTP.new(uri.host, uri.port)
  http.use_ssl = true

  request = Net::HTTP::Post.new(uri)
  request['Authorization'] = "Bearer #{ENV.fetch('SLACK_BOT_TOKEN', nil)}"
  request['Content-Type'] = 'application/json'

  request.body = {
    trigger_id: trigger_id,
    view: modal_view(schedule)
  }.to_json

  response = http.request(request)
  result = JSON.parse(response.body)

  return if result['ok']

  settings.logger.error('open_modal_failed', error: result['error'])
end

def modal_view(schedule = nil)
  {
    type: 'modal',
    callback_id: 'schedule_modal',
    title: {
      type: 'plain_text',
      text: 'Ukentlig status'
    },
    submit: {
      type: 'plain_text',
      text: 'Lagre'
    },
    close: {
      type: 'plain_text',
      text: 'Avbryt'
    },
    blocks: [
      {
        type: 'section',
        text: {
          type: 'mrkdwn',
          text: 'Sett opp din ukentlige statusplan! For hver dag kan du legge til en fast beskrivelse, ' \
                'og "velge" en emoji 🎉'
        }
      },
      { type: 'divider' },
      *day_blocks('monday', 'Mandag', schedule),
      *day_blocks('tuesday', 'Tirsdag', schedule),
      *day_blocks('wednesday', 'Onsdag', schedule),
      *day_blocks('thursday', 'Torsdag', schedule),
      *day_blocks('friday', 'Fredag', schedule),
      { type: 'divider' },
      {
        type: 'section',
        text: {
          type: 'mrkdwn',
          text: '*Vil du slutte?* Hvis du ikke lenger ønsker å motta daglige statuser kan du avmelde deg.'
        },
        accessory: {
          type: 'button',
          text: {
            type: 'plain_text',
            text: 'Avmeld',
            emoji: true
          },
          style: 'danger',
          action_id: 'unsubscribe_button'
        }
      }
    ]
  }
end

def day_blocks(day, label, schedule = nil)
  day_data = schedule&.dig(day)
  text_value = day_data&.dig('text')
  emoji_value = day_data&.dig('emoji')

  text_element = {
    type: 'plain_text_input',
    action_id: "#{day}_text_input",
    placeholder: {
      type: 'plain_text',
      text: 'f.eks. Hjemmekontor'
    }
  }
  text_element[:initial_value] = text_value if text_value

  emoji_element = {
    type: 'rich_text_input',
    action_id: "#{day}_emoji_input"
  }
  if emoji_value
    emoji_name = emoji_value.delete(':')
    emoji_element[:initial_value] = {
      type: 'rich_text',
      elements: [
        {
          type: 'rich_text_section',
          elements: [
            { type: 'emoji', name: emoji_name }
          ]
        }
      ]
    }
  end

  [
    {
      type: 'header',
      text: {
        type: 'plain_text',
        text: label
      }
    },
    {
      type: 'input',
      block_id: "#{day}_text",
      label: {
        type: 'plain_text',
        text: 'Statusbeskrivelse'
      },
      element: text_element
    },
    {
      type: 'input',
      block_id: "#{day}_emoji",
      label: {
        type: 'plain_text',
        text: 'Statusemoji'
      },
      element: emoji_element
    }
  ]
end

def handle_form_submission(payload)
  user_id = payload['user']['id']
  values = payload['view']['state']['values']

  schedule = %w[monday tuesday wednesday thursday friday].each_with_object({}) do |day, hash|
    hash[day] = {
      'text' => values["#{day}_text"]["#{day}_text_input"]['value'],
      'emoji' => extract_emoji(values["#{day}_emoji"]["#{day}_emoji_input"])
    }
  end

  settings.valkey.save_schedule(user_id, schedule)

  content_type :json
  { response_action: 'clear' }.to_json
end

# Extract the first emoji from a rich_text_input value.
# Payload structure: { "type" => "rich_text", "elements" => [
#   { "type" => "rich_text_section", "elements" => [
#     { "type" => "emoji", "name" => "house" }
#   ]}
# ]}
def extract_emoji(rich_text)
  return nil unless rich_text

  elements = rich_text.dig('rich_text_value', 'elements') || []
  elements.each do |section|
    next unless section['elements']

    section['elements'].each do |el|
      return ":#{el['name']}:" if el['type'] == 'emoji'
    end
  end

  nil
end

def handle_block_actions(payload)
  user_id = payload['user'] && payload['user']['id']
  action = payload['actions']&.first
  return unless user_id && action
  return unless action['action_id'] == 'unsubscribe_button'

  process_unsubscribe(user_id)

  content_type :json
  body ''
end

def process_unsubscribe(user_id)
  settings.valkey.delete_schedule(user_id)
  settings.logger.info('user_unsubscribed', user_id: user_id)
  dm_result = settings.slack.send_dm(user_id, 'Du er nå avmeldt fra en god morgen.')
  if dm_result && dm_result['ok']
    settings.logger.info('user_unsubscribed_dm_sent', user_id: user_id)
  else
    settings.logger.error('user_unsubscribed_dm_failed', user_id: user_id, error: dm_result && dm_result['error'],
                                                         payload: dm_result)
  end
end

post '/api/apply-statuses' do
  content_type :json

  today = Date.today.strftime('%A').downcase
  user_ids = settings.valkey.all_user_ids
  settings.logger.info('apply_statuses', user_count: user_ids.size)
  applied = 0

  user_ids.each do |user_id|
    schedule = settings.valkey.get_schedule(user_id)
    next unless schedule

    status_config = schedule[today]
    next unless status_config

    result = settings.slack.set_status(user_id, status_config['text'], status_config['emoji'])

    if result['ok']
      settings.logger.info('set_status_response', user_id: user_id)

      dm_result = settings.slack.send_dm(user_id,
                                         'God morgen! Status satt til ' \
                                         "#{status_config['emoji']} #{status_config['text']}")
      if dm_result['ok']
        settings.logger.info('send_dm_response', user_id: user_id)
        applied += 1
      else
        settings.logger.error('send_dm_response', user_id: user_id, error: dm_result['error'], payload: dm_result)
      end
    else

      settings.logger.error('set_status_response', user_id: user_id, error: result['error'], payload: dm_result)
    end
  end

  { applied: applied, users: user_ids.size }.to_json
end
