resource "aws_cloudfront_function" "reject_unadmitted_holds" {
  name    = "${var.name_prefix}-reject-unadmitted-holds"
  runtime = "cloudfront-js-2.0"
  comment = "Edge pre-filter: reject POST .../holds with no X-Admission-Token before it costs a Lambda invocation. NOT the authoritative admission check — see the function source comment."
  publish = true
  code    = file("${path.module}/functions/reject-unadmitted-holds.js")
}
