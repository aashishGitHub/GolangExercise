output "layouts_bucket" {
  value = aws_s3_bucket.layouts.id
}

output "layouts_bucket_arn" {
  value = aws_s3_bucket.layouts.arn
}

output "layouts_cloudfront_domain" {
  value = aws_cloudfront_distribution.layouts.domain_name
}

output "tickets_bucket" {
  value = aws_s3_bucket.tickets.id
}

output "tickets_bucket_arn" {
  value = aws_s3_bucket.tickets.arn
}

output "web_bucket" {
  value = aws_s3_bucket.web.id
}

output "web_cloudfront_domain" {
  value = aws_cloudfront_distribution.web.domain_name
}
