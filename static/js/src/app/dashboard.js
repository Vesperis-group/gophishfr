var campaigns = []
// statuses maps result statuses to UI metadata.
var statuses = {
    "Email Sent": {
        color: "#1abc9c",
        label: "label-success",
        icon: "fa-envelope"
    },
    "Emails Sent": {
        color: "#1abc9c",
        label: "label-success",
        icon: "fa-envelope"
    },
    "In progress": {
        label: "label-primary"
    },
    "Queued": {
        label: "label-info"
    },
    "Completed": {
        label: "label-success"
    },
    "Email Opened": {
        color: "#f9bf3b",
        label: "label-warning",
        icon: "fa-envelope"
    },
    "Email Reported": {
        color: "#45d6ef",
        label: "label-warning",
        icon: "fa-bullhorne"
    },
    "Clicked Link": {
        color: "#F39C12",
        label: "label-clicked",
        icon: "fa-mouse-pointer"
    },
    "Success": {
        color: "#f05b4f",
        label: "label-danger",
        icon: "fa-exclamation"
    },
    "Error": {
        color: "#6c7a89",
        label: "label-default",
        icon: "fa-times"
    },
    "Error Sending Email": {
        color: "#6c7a89",
        label: "label-default",
        icon: "fa-times"
    },
    "Submitted Data": {
        color: "#f05b4f",
        label: "label-danger",
        icon: "fa-exclamation"
    },
    "Unknown": {
        color: "#6c7a89",
        label: "label-default",
        icon: "fa-question"
    },
    "Sending": {
        color: "#428bca",
        label: "label-primary",
        icon: "fa-spinner"
    },
    "Campaign Created": {
        label: "label-success",
        icon: "fa-rocket"
    }
}

var statsMapping = {
    "sent": "Email Sent",
    "opened": "Email Opened",
    "email_reported": "Email Reported",
    "clicked": "Clicked Link",
    "submitted_data": "Submitted Data",
}

function deleteCampaign(idx) {
    if (confirm("Delete " + campaigns[idx].name + "?")) {
        api.campaignId.delete(campaigns[idx].id)
            .success(function (data) {
                successFlash(data.message)
                location.reload()
            })
    }
}

function renderPieChart(chartopts) {
    return GophishCharts.renderDoughnut({
        elemId: chartopts['elemId'],
        title: chartopts['title'],
        data: chartopts['data'],
        colors: chartopts['colors'],
        centerFontSize: 16
    })
}

function generateStatsPieCharts(campaigns) {
    var stats_data = []
    var stats_series_data = {}
    var total = 0

    $.each(campaigns, function (i, campaign) {
        $.each(campaign.stats, function (status, count) {
            if (status == "total") {
                total += count
                return true
            }
            if (!stats_series_data[status]) {
                stats_series_data[status] = count;
            } else {
                stats_series_data[status] += count;
            }
        })
    })
    $.each(stats_series_data, function (status, count) {
        // I don't like this, but I guess it'll have to work.
        // Turns submitted_data into Submitted Data
        if (!(status in statsMapping)) {
            return true
        }
        status_label = statsMapping[status]
        stats_data.push({
            name: status_label,
            y: Math.floor((count / total) * 100),
            count: count
        })
        stats_data.push({
            name: '',
            y: 100 - Math.floor((count / total) * 100)
        })
        var stats_chart = renderPieChart({
            elemId: status + '_chart',
            title: status_label,
            name: status,
            data: stats_data,
            colors: [statuses[status_label].color, "#dddddd"]
        })

        stats_data = []
    });
}

function generateTimelineChart(campaigns) {
    var overview_data = []
    $.each(campaigns, function (i, campaign) {
        var campaign_date = moment.utc(campaign.created_date).local()
        // Add it to the chart data
        campaign.y = 0
        // Clicked events also contain our data submitted events
        campaign.y += campaign.stats.clicked
        campaign.y = Math.floor((campaign.y / campaign.stats.total) * 100)
        // Add the data to the overview chart
        overview_data.push({
            campaign_id: campaign.id,
            name: campaign.name,
            x: campaign_date.valueOf(),
            y: campaign.y
        })
    })
    GophishCharts.renderOverview({
        elemId: 'overview_chart',
        title: 'Phishing Success Overview',
        data: overview_data
    })
}

$(document).ready(function () {
    api.campaigns.summary()
        .success(function (data) {
            $("#loading").hide()
            campaigns = data.campaigns
            if (campaigns.length > 0) {
                $("#dashboard").show()
                // Create the overview chart data
                campaignTable = $("#campaignTable").DataTable({
                    columnDefs: [{
                            orderable: false,
                            targets: "no-sort"
                        },
                        {
                            className: "color-sent",
                            targets: [2]
                        },
                        {
                            className: "color-opened",
                            targets: [3]
                        },
                        {
                            className: "color-clicked",
                            targets: [4]
                        },
                        {
                            className: "color-success",
                            targets: [5]
                        },
                        {
                            className: "color-reported",
                            targets: [6]
                        }
                    ],
                    order: [
                        [1, "desc"]
                    ]
                });
                campaignRows = []
                $.each(campaigns, function (i, campaign) {
                    var campaign_date = moment(campaign.created_date).format('MMMM Do YYYY, h:mm:ss a')
                    var label = statuses[campaign.status].label || "label-default";
                    //section for tooltips on the status of a campaign to show some quick stats
                    var launchDate;
                    if (moment(campaign.launch_date).isAfter(moment())) {
                        launchDate = "Scheduled to start: " + moment(campaign.launch_date).format('MMMM Do YYYY, h:mm:ss a')
                        var quickStats = launchDate + "<br><br>" + "Number of recipients: " + campaign.stats.total
                    } else {
                        launchDate = "Launch Date: " + moment(campaign.launch_date).format('MMMM Do YYYY, h:mm:ss a')
                        var quickStats = launchDate + "<br><br>" + "Number of recipients: " + campaign.stats.total + "<br><br>" + "Emails opened: " + campaign.stats.opened + "<br><br>" + "Emails clicked: " + campaign.stats.clicked + "<br><br>" + "Submitted Credentials: " + campaign.stats.submitted_data + "<br><br>" + "Errors : " + campaign.stats.error + "<br><br>" + "Reported : " + campaign.stats.email_reported
                    }
                    // Add it to the list
                    campaignRows.push([
                        escapeHtml(campaign.name),
                        campaign_date,
                        campaign.stats.sent,
                        campaign.stats.opened,
                        campaign.stats.clicked,
                        campaign.stats.submitted_data,
                        campaign.stats.email_reported,
                        "<span class=\"label " + label + "\" data-toggle=\"tooltip\" data-placement=\"right\" data-html=\"true\" title=\"" + quickStats + "\">" + campaign.status + "</span>",
                        "<div class='pull-right'><a class='btn btn-primary' href='/campaigns/" + campaign.id + "' data-toggle='tooltip' data-placement='left' title='View Results'>\
                    <i class='fa fa-bar-chart'></i>\
                    </a>\
                    <button class='btn btn-danger' onclick='deleteCampaign(" + i + ")' data-toggle='tooltip' data-placement='left' title='Delete Campaign'>\
                    <i class='fa fa-trash-o'></i>\
                    </button></div>"
                    ])
                    $('[data-toggle="tooltip"]').tooltip()
                })
                campaignTable.rows.add(campaignRows).draw()
                // Build the charts
                generateStatsPieCharts(campaigns)
                generateTimelineChart(campaigns)
            } else {
                $("#emptyMessage").show()
            }
        })
        .error(function () {
            errorFlash("Error fetching campaigns")
        })
})
