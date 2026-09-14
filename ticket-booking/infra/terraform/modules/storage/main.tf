# docs/plan.md "storage" module: three buckets with three DIFFERENT
# serving policies, deliberately not uniform:
#  - layouts:  public, immutable, CloudFront + Origin Access Control
#  - tickets:  private, NO CDN -- a CDN would cache/serve past a presigned
#              URL's expiry, defeating the whole point of a short TTL
#              (internal/ticketing.PresignTTL = 60s locally)
#  - web:      the SPA bundle, CloudFront-fronted, 403/404 -> index.html
#              for client-side routing

resource "aws_s3_bucket" "layouts" {
  bucket = "${var.name_prefix}-layouts"
}

resource "aws_s3_bucket_versioning" "layouts" {
  bucket = aws_s3_bucket.layouts.id
  versioning_configuration {
    status = "Enabled" # layoutVersion is in the PATH (docs/plan.md), but
    # versioning is still cheap insurance against an
    # accidental overwrite of an immutable object
  }
}

resource "aws_cloudfront_origin_access_control" "layouts" {
  name                              = "${var.name_prefix}-layouts-oac"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

data "aws_iam_policy_document" "layouts_cloudfront_read" {
  statement {
    sid       = "AllowCloudFrontServicePrincipalReadOnly"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.layouts.arn}/*"]

    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.layouts.arn]
    }
  }
}

resource "aws_s3_bucket_policy" "layouts" {
  bucket = aws_s3_bucket.layouts.id
  policy = data.aws_iam_policy_document.layouts_cloudfront_read.json
}

resource "aws_cloudfront_distribution" "layouts" {
  enabled = true
  comment = "${var.name_prefix} venue layouts (immutable, 1y cache)"

  origin {
    domain_name              = aws_s3_bucket.layouts.bucket_regional_domain_name
    origin_id                = "layouts-s3"
    origin_access_control_id = aws_cloudfront_origin_access_control.layouts.id
  }

  default_cache_behavior {
    target_origin_id       = "layouts-s3"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    cache_policy_id        = data.aws_cloudfront_cache_policy.caching_optimized.id
    compress               = true
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }
}

data "aws_cloudfront_cache_policy" "caching_optimized" {
  name = "Managed-CachingOptimized"
}

# tickets: private, NO CloudFront resource at all -- the absence is the
# point (docs/plan.md storage module comment, verbatim).
resource "aws_s3_bucket" "tickets" {
  bucket = "${var.name_prefix}-tickets"
}

resource "aws_s3_bucket_public_access_block" "tickets" {
  bucket                  = aws_s3_bucket.tickets.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "tickets" {
  bucket = aws_s3_bucket.tickets.id
  rule {
    id     = "expire-old-qr-images"
    status = "Enabled"
    filter {} # applies to every object in the bucket
    expiration {
      days = 400 # well past any real event's date; a cheap cleanup net,
      # not a security control (redemption is enforced in
      # Postgres, not by the image disappearing)
    }
  }
}

# web: the SPA bundle.
resource "aws_s3_bucket" "web" {
  bucket = "${var.name_prefix}-web"
}

resource "aws_cloudfront_origin_access_control" "web" {
  name                              = "${var.name_prefix}-web-oac"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

data "aws_iam_policy_document" "web_cloudfront_read" {
  statement {
    sid       = "AllowCloudFrontServicePrincipalReadOnly"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.web.arn}/*"]

    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.web.arn]
    }
  }
}

resource "aws_s3_bucket_policy" "web" {
  bucket = aws_s3_bucket.web.id
  policy = data.aws_iam_policy_document.web_cloudfront_read.json
}

resource "aws_cloudfront_distribution" "web" {
  enabled             = true
  comment             = "${var.name_prefix} SPA"
  default_root_object = "index.html"

  origin {
    domain_name              = aws_s3_bucket.web.bucket_regional_domain_name
    origin_id                = "web-s3"
    origin_access_control_id = aws_cloudfront_origin_access_control.web.id
  }

  default_cache_behavior {
    target_origin_id       = "web-s3"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    cache_policy_id        = data.aws_cloudfront_cache_policy.caching_optimized.id
    compress               = true
  }

  # Client-side routing: any path S3 doesn't have (a deep link like
  # /events/42) falls through to index.html so the SPA's own router takes
  # over, instead of a CloudFront/S3 XML error page.
  custom_error_response {
    error_code         = 403
    response_code      = 200
    response_page_path = "/index.html"
  }
  custom_error_response {
    error_code         = 404
    response_code      = 200
    response_page_path = "/index.html"
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }
}
