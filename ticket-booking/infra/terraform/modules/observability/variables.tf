variable "name_prefix" {
  type = string
}

variable "consumer_dlq_names" {
  type = map(string) # key -> SQS queue name (not ARN — CloudWatch metrics key by name)
}

variable "rds_proxy_name" {
  type = string
}

variable "redis_replication_group_id" {
  type = string
}

variable "alarm_sns_topic_arn" {
  type    = string
  default = null # nullable: alarms with no action still show red in the console/dashboard
}
