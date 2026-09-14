output "photos_bucket_name" {
  value = aws_s3_bucket.photos.id
}
output "photos_bucket_arn" {
  value = aws_s3_bucket.photos.arn
}
output "photos_cloudfront_domain" {
  value = aws_cloudfront_distribution.photos.domain_name
}

output "web_bucket_name" {
  value = aws_s3_bucket.web.id
}
output "web_cloudfront_domain" {
  value = aws_cloudfront_distribution.web.domain_name
}
